package loadbalance

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	testutil "github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
)

const oneDomainWRR = `
w1,example.org
192.168.1.15 10
192.168.1.14 20
`

var testOneDomainWRR = map[string]*domain{
	"w1,example.org.": &domain{
		weights: []*weightItem{
			&weightItem{net.ParseIP("192.168.1.14"), uint8(20)},
			&weightItem{net.ParseIP("192.168.1.15"), uint8(10)},
		},
		topIPupdater: &randomizedWRR{wsum: uint(30)},
	},
}

const twoDomainsWRR = `
# domain 1
w1.example.org
192.168.1.15   10
192.168.1.14   20

 # domain 2
 w2.example.org
 192.168.2.16 11
 192.168.2.15 12
 192.168.2.14 13
`

var testTwoDomainsWRR = map[string]*domain{
	"w1.example.org.": &domain{
		weights: []*weightItem{
			&weightItem{net.ParseIP("192.168.1.14"), uint8(20)},
			&weightItem{net.ParseIP("192.168.1.15"), uint8(10)},
		},
		topIPupdater: &randomizedWRR{wsum: uint(30)},
	},
	"w2.example.org.": &domain{
		weights: []*weightItem{
			&weightItem{net.ParseIP("192.168.2.14"), uint8(13)},
			&weightItem{net.ParseIP("192.168.2.15"), uint8(12)},
			&weightItem{net.ParseIP("192.168.2.16"), uint8(11)},
		},
		topIPupdater: &randomizedWRR{wsum: uint(36)},
	},
}

const missingWeightWRR = `
w1,example.org
192.168.1.14
192.168.1.15 20
`

const missingDomainWRR = `
# missing domain
192.168.1.14 10
w2,example.org
192.168.2.14 11
192.168.2.15 12
`

const wrongIpWRR = `
w1,example.org
192.168.1.300 10
`

const wrongWeightWRR = `
w1,example.org
192.168.1.14 300
`

func TestWeightFileUpdate(t *testing.T) {
	tests := []struct {
		weightFilContent   string
		shouldErr          bool
		expectedDomains    map[string]*domain
		expectedErrContent string // substring from the expected error. Empty for positive cases.
	}{
		// positive
		{"", false, nil, ""},
		{oneDomainWRR, false, testOneDomainWRR, ""},
		{twoDomainsWRR, false, testTwoDomainsWRR, ""},
		// negative
		{missingWeightWRR, true, nil, "Wrong domain name"},
		{missingDomainWRR, true, nil, "Missing domain name"},
		{wrongIpWRR, true, nil, "Wrong IP address"},
		{wrongWeightWRR, true, nil, "Wrong weight value"},
	}

	for i, test := range tests {
		testFile, rm, err := testutil.TempFile(".", test.weightFilContent)
		if err != nil {
			t.Fatal(err)
		}
		defer rm()
		weighted := &weightedRR{fileName: testFile, isRandom: true, rn: rand.New(rand.NewSource(1))}
		err = weighted.updateWeights()
		if test.shouldErr && err == nil {
			t.Errorf("Test %d: Expected error but found %s", i, err)
		}
		if err != nil {
			if !test.shouldErr {
				t.Errorf("Test %d: Expected no error but found error: %v", i, err)
			}

			if !strings.Contains(err.Error(), test.expectedErrContent) {
				t.Errorf("Test %d: Expected error to contain: %v, found error: %v",
					i, test.expectedErrContent, err)
			}
		}
		if test.expectedDomains != nil {
			if len(test.expectedDomains) != len(weighted.domains) {
				t.Errorf("Test %d: Expected len(domains): %d but got %d",
					i, len(test.expectedDomains), len(weighted.domains))
			} else {
				_ = checkDomainsWRR(t, i, test.expectedDomains, weighted.domains)
			}
		}
	}
}

func checkDomainsWRR(t *testing.T, testIndex int, expectedDomains, domains map[string]*domain) error {
	var ret error
	retError := errors.New("Check domains failed")
	for dname, expectedDomain := range expectedDomains {
		domain, ok := domains[dname]
		if !ok {
			t.Errorf("Test %d: Expected domain %s but not found it", testIndex, dname)
			ret = retError
		} else {
			expectedWeights := expectedDomain.weights
			weights := domain.weights
			if len(expectedWeights) != len(weights) {
				t.Errorf("Test %d: Expected len(weights): %d for domain %s but got %d",
					testIndex, len(expectedWeights), dname, len(weights))
				ret = retError
			} else {
				for i, w := range expectedWeights {
					if !w.address.Equal(weights[i].address) || w.value != weights[i].value {
						t.Errorf("Test %d: Weight list differs at index %d for domain %s. "+
							"Expected: %v got: %v", testIndex, i, dname, expectedWeights[i], weights[i])
						ret = retError
					}
				}
				expectedTopIP, ok1 := expectedDomain.topIPupdater.(*randomizedWRR)
				topIP, ok2 := domain.topIPupdater.(*randomizedWRR)
				if !ok2 {
					t.Errorf("Test %d: Expected randomized WRR for domain %s", testIndex, dname)
					ret = retError
				} else if ok1 && expectedTopIP.wsum != topIP.wsum {
					t.Errorf("Test %d: Expected weight sum %d but got %d for domain %s", testIndex,
						expectedTopIP.wsum, topIP.wsum, dname)
					ret = retError
				}
			}
		}
	}

	return ret
}

func TestPeriodicWeightUpdate(t *testing.T) {
	testFile1, rm, err := testutil.TempFile(".", oneDomainWRR)
	if err != nil {
		t.Fatal(err)
	}
	defer rm()
	testFile2, rm, err := testutil.TempFile(".", twoDomainsWRR)
	if err != nil {
		t.Fatal(err)
	}
	defer rm()

	// configure weightedRR with "oneDomainWRR" weight file content
	weighted := &weightedRR{fileName: testFile1, isRandom: true, rn: rand.New(rand.NewSource(1))}

	err = weighted.updateWeights()
	if err != nil {
		t.Fatal(err)
	} else {
		err = checkDomainsWRR(t, 0, testOneDomainWRR, weighted.domains)
		if err != nil {
			t.Fatalf("Initial check domains failed")
		}
	}

	// change weight file
	weighted.fileName = testFile2
	// start periodic update
	weighted.reload = 10 * time.Millisecond
	stopChan := make(chan bool)
	weighted.periodicWeightUpdate(stopChan)
	time.Sleep(20 * time.Millisecond)
	// stop periodic update
	close(stopChan)
	// check updated config
	weighted.mutex.Lock()
	err = checkDomainsWRR(t, 0, testTwoDomainsWRR, weighted.domains)
	weighted.mutex.Unlock()
	if err != nil {
		t.Fatalf("Final check domains failed")
	}
}

func TestLoadBalanceWRR(t *testing.T) {
	weighted := &weightedRR{}

	// We test randomWRR in determinstic mode
	rm := RoundRobin{Next: handler(), policy: weightedRoundRobinPolicy, weights: weighted}

	type testQuery struct {
		name        string // domain name to query
		answerTopIP string // top (first) address record in the answer. Empty if no change is expected.
		extraTopIP  string // top (first) address record in the extra section. Empty if no change is expected.
	}

	// domain maps to test
	oneDomain := map[string]*domain{
		"endpoint.region2.skydns.test": &domain{
			weights: []*weightItem{
				&weightItem{net.ParseIP("10.240.0.2"), uint8(2)},
				&weightItem{net.ParseIP("10.240.0.1"), uint8(1)},
			},
		},
	}
	twoDomains := map[string]*domain{
		"endpoint.region2.skydns.test": &domain{
			weights: []*weightItem{
				&weightItem{net.ParseIP("10.240.0.2"), uint8(2)},
				&weightItem{net.ParseIP("10.240.0.1"), uint8(1)},
			},
		},
		"endpoint.region1.skydns.test": &domain{
			weights: []*weightItem{
				&weightItem{net.ParseIP("::2"), uint8(3)},
				&weightItem{net.ParseIP("::1"), uint8(2)},
			},
		},
	}

	// the first X records must be cnames after this test
	tests := []struct {
		answer        []dns.RR
		extra         []dns.RR
		cnameAnswer   int
		cnameExtra    int
		addressAnswer int
		addressExtra  int
		mxAnswer      int
		mxExtra       int
		domains       map[string]*domain
		queries       []testQuery
	}{
		{
			answer: []dns.RR{
				testutil.CNAME("cname1.region2.skydns.test.	300	IN	CNAME		cname2.region2.skydns.test."),
				testutil.CNAME("cname2.region2.skydns.test.	300	IN	CNAME		cname3.region2.skydns.test."),
				testutil.CNAME("cname5.region2.skydns.test.	300	IN	CNAME		cname6.region2.skydns.test."),
				testutil.CNAME("cname6.region2.skydns.test.	300	IN	CNAME		endpoint.region2.skydns.test."),
				testutil.A("endpoint.region2.skydns.test.		300	IN	A			10.240.0.1"),
				testutil.A("endpoint.region2.skydns.test.	    300	IN	A			10.240.0.2"),
				testutil.A("endpoint.region2.skydns.test.	    300	IN	A			10.240.0.3"),
				testutil.AAAA("endpoint.region1.skydns.test.	300	IN	AAAA		::1"),
				testutil.AAAA("endpoint.region1.skydns.test.	300	IN	AAAA		::2"),
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		1	mx1.region2.skydns.test."),
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		2	mx2.region2.skydns.test."),
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		3	mx3.region2.skydns.test."),
			},
			cnameAnswer:   4,
			addressAnswer: 5,
			mxAnswer:      3,
			domains:       twoDomains,
			queries: []testQuery{
				{"endpoint.region2.skydns.test", "10.240.0.2", ""}, // domain 1 weight 2 - first time
				{"w1.region3.skydns.test", "", ""},                 // domain is not in the weight file -> no change
				{"endpoint.region2.skydns.test", "10.240.0.2", ""}, // domain 1 weight 2 -  second time
				{"endpoint.region1.skydns.test", "::2", ""},        // domain 2 weight 3 - first time
				{"endpoint.region2.skydns.test", "10.240.0.1", ""}, // domain 1 weight 1 - first time
				{"endpoint.region1.skydns.test", "::2", ""},        // domain 2 weight 3 - second time
				{"endpoint.region2.skydns.test", "10.240.0.2", ""}, // weight 2 - first time
				{"endpoint.region1.skydns.test", "::2", ""},        // domain 2 weight 3 - third time
				{"endpoint.region2.skydns.test", "10.240.0.2", ""}, // weight 2 - second time
				{"endpoint.region1.skydns.test", "::1", ""},        // domain 2 weight 2 - first time
			},
		},
		{
			answer: []dns.RR{
				testutil.A("endpoint.region2.skydns.test.		300	IN	A			10.240.0.3"),
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		1	mx1.region2.skydns.test."),
				testutil.CNAME("cname.region2.skydns.test.	300	IN	CNAME		endpoint.region2.skydns.test."),
			},
			cnameAnswer:   1,
			addressAnswer: 1,
			mxAnswer:      1,
			domains:       oneDomain,
			queries: []testQuery{
				{"endpoint.region2.skydns.test", "", ""}, // no domains - empty weight file -> no change
				{"endpoint.region2.skydns.test", "", ""}, // IP is not in the address list -> no change
				{"w1.region1.skydns.test", "", ""},       // domain is not in the weight file -> no change
			},
		},
		{
			answer: []dns.RR{
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		1	mx1.region2.skydns.test."),
				testutil.A("endpoint.region2.skydns.test.		300	IN	A			10.240.0.1"),
				testutil.A("endpoint.region2.skydns.test.		300	IN	A			10.240.0.2"),
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		1	mx2.region2.skydns.test."),
				testutil.CNAME("cname2.region2.skydns.test.	300	IN	CNAME		cname3.region2.skydns.test."),
				testutil.A("endpoint.region2.skydns.test.		300	IN	A			10.240.0.3"),
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		1	mx3.region2.skydns.test."),
			},
			extra: []dns.RR{
				testutil.AAAA("endpoint.region2.skydns.test.	300	IN	AAAA		::1"),
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		1	mx1.region2.skydns.test."),
				testutil.CNAME("cname2.region2.skydns.test.	300	IN	CNAME		cname3.region2.skydns.test."),
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		1	mx2.region2.skydns.test."),
				testutil.AAAA("endpoint.region2.skydns.test.	300	IN	AAAA		::2"),
				testutil.MX("mx.region2.skydns.test.			300	IN	MX		1	mx3.region2.skydns.test."),
			},
			cnameAnswer:   1,
			cnameExtra:    1,
			addressAnswer: 3,
			addressExtra:  2,
			mxAnswer:      3,
			mxExtra:       3,
			domains:       twoDomains,
			queries: []testQuery{
				{"endpoint.region2.skydns.test", "10.240.0.2", ""}, // domain 1 weight 2 - first time
				{"w1.region1.skydns.test", "", ""},                 // domain is not in the weight file -> no change
				{"endpoint.region1.skydns.test", "", "::2"},        // domain 2 weight 3 -  first time
				{"endpoint.region2.skydns.test", "10.240.0.2", ""}, // domain 1 weight 2 -  second time
				{"endpoint.region1.skydns.test", "", "::2"},        // domain 2 weight 3 -  second time
				{"endpoint.region2.skydns.test", "10.240.0.1", ""}, // domain 1 weight 1 - first time
				{"endpoint.region1.skydns.test", "", "::2"},        // domain 2 weight 3 -  third time
				{"endpoint.region2.skydns.test", "10.240.0.2", ""}, // weight 2 - first time
				{"endpoint.region1.skydns.test", "", "::1"},        // domain 2 weight 2 - first time
			},
		},
	}

	rec := dnstest.NewRecorder(&testutil.ResponseWriter{})

	for i, test := range tests {
		// set domain map for weighted round robin
		rm.weights.domains = test.domains
		for _, d := range rm.weights.domains {
			d.topIPupdater = &determininsticWRR{} // deterministic mode for testing
			d.topIPupdater.nextTopIP(d, nil)      // initialize the expected "top" IP
		}

		for j, query := range test.queries {
			req := new(dns.Msg)
			req.SetQuestion(query.name, dns.TypeSRV)
			req.Answer = test.answer
			req.Extra = test.extra

			_, err := rm.ServeDNS(context.TODO(), rec, req)
			if err != nil {
				t.Errorf("Test %d: Expected no error, but got %s", i, err)
				continue
			}

			if query.answerTopIP != "" {
				checkTopIP(t, i, j, rec.Msg.Answer, query.answerTopIP)
			}

			if query.extraTopIP != "" {
				checkTopIP(t, i, j, rec.Msg.Extra, query.extraTopIP)
			}

			cname, address, mx, sorted := countRecords(rec.Msg.Answer)
			if query.answerTopIP != "" && !sorted {
				t.Errorf("Test %d query %d: Expected CNAMEs, then AAAAs, then MX in Answer, but got mixed", i, j)
			}
			if cname != test.cnameAnswer {
				t.Errorf("Test %d query %d: Expected %d CNAMEs in Answer, but got %d", i, j, test.cnameAnswer, cname)
			}
			if address != test.addressAnswer {
				t.Errorf("Test %d query %d: Expected %d A/AAAAs in Answer, but got %d", i, j, test.addressAnswer, address)
			}
			if mx != test.mxAnswer {
				t.Errorf("Test %d query %d: Expected %d MXs in Answer, but got %d", i, j, test.mxAnswer, mx)
			}

			cname, address, mx, sorted = countRecords(rec.Msg.Extra)
			if query.extraTopIP != "" && !sorted {
				t.Errorf("Test %d query %d: Expected CNAMEs, then AAAAs, then MX in Answer, but got mixed", i, j)
			}

			if cname != test.cnameExtra {
				t.Errorf("Test %d query %d: Expected %d CNAMEs in Extra, but got %d", i, j, test.cnameAnswer, cname)
			}
			if address != test.addressExtra {
				t.Errorf("Test %d query %d: Expected %d A/AAAAs in Extra, but got %d", i, j, test.addressAnswer, address)
			}
			if mx != test.mxExtra {
				t.Errorf("Test %d query %d: Expected %d MXs in Extra, but got %d", i, j, test.mxAnswer, mx)
			}
		}
	}
}

func checkTopIP(t *testing.T, i, j int, result []dns.RR, expectedTopIP string) {
	expected := net.ParseIP(expectedTopIP)
	for _, r := range result {
		switch r.Header().Rrtype {
		case dns.TypeA:
			ar := r.(*dns.A)
			if !ar.A.Equal(expected) {
				t.Errorf("Test %d query %d: expected top IP %s but got %s", i, j, expectedTopIP, ar.A)
			}
			return
		case dns.TypeAAAA:
			ar := r.(*dns.AAAA)
			if !ar.AAAA.Equal(expected) {
				t.Errorf("Test %d query %d: expected top IP %s but got %s", i, j, expectedTopIP, ar.AAAA)
			}
			return
		}
	}
	t.Errorf("Test %d query %d: expected top IP %s but got no address records", i, j, expectedTopIP)
}

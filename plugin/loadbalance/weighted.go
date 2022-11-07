package loadbalance

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
)

func (w *weightedRR) weightedRoundRobin(qname string, in []dns.RR) []dns.RR {
	cname := []dns.RR{}
	address := []dns.RR{}
	mx := []dns.RR{}
	rest := []dns.RR{}
	for _, r := range in {
		switch r.Header().Rrtype {
		case dns.TypeCNAME:
			cname = append(cname, r)
		case dns.TypeA, dns.TypeAAAA:
			address = append(address, r)
		case dns.TypeMX:
			mx = append(mx, r)
		default:
			rest = append(rest, r)
		}
	}

	if len(address) == 0 || !w.setTopRecord(qname, address) {
		// no change
		return in
	}

	out := append(cname, rest...)
	out = append(out, address...)
	out = append(out, mx...)
	return out
}

// Move the next expected address to the first position in the result list
func (w *weightedRR) setTopRecord(qname string, address []dns.RR) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	curd, ok := w.domains[qname]
	if !ok || len(curd.weights) == 0 {
		// no weights list
		return false
	}

	expTopIP := curd.topIP
	itop := -1

L:
	for i, r := range address {
		switch r.Header().Rrtype {
		case dns.TypeA:
			ar := r.(*dns.A)
			if ar.A.Equal(expTopIP) {
				itop = i
				break L
			}
		case dns.TypeAAAA:
			ar := r.(*dns.AAAA)
			if ar.AAAA.Equal(expTopIP) {
				itop = i
				break L
			}
		}
	}

	if itop == -1 {
		// Expected top entry is not in the list
		return false
	}

	if itop != 0 {
		// swap expected top entry with the actual one
		address[0], address[itop] = address[itop], address[0]
	}
	// move to the next expected "top" IP
	curd.nextTopIP(w.rn)

	return true
}

// Compute the next expected top (first) IP
func (d *domain) nextTopIP(rn *rand.Rand) {
	v := rn.Intn(int(d.wsum))

	psum := 0
	var w *weightItem
	for _, w = range d.weights {
		psum += int(w.value)
		if v < psum {
			break
		}
	}
	d.topIP = w.address
}

// Start go routine to update weights from the weight file periodically
func (w *weightedRR) periodicWeightUpdate(stopReload <-chan bool) {

	if w.reload == 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(w.reload)
		for {
			select {
			case <-stopReload:
				return
			case <-ticker.C:
				err := w.updateWeights()
				if err != nil {
					log.Error(err)
				}
			}
		}
	}()
}

// Update weights from weight file
func (w *weightedRR) updateWeights() error {
	// access to weights must be protected
	w.mutex.Lock()
	defer w.mutex.Unlock()

	isChanged, err := w.readWeightFile()
	if err != nil || !isChanged {
		return err
	}

	// Sort weight litst. First elements will have max weight.
	for _, d := range w.domains {
		sort.Slice(d.weights, func(i, j int) bool {
			return d.weights[i].value > d.weights[j].value
		})
		// Calculate the sum of weights per domain
		for _, w := range d.weights {
			d.wsum += uint(w.value)
		}
		// initialize first expected "top" IP
		d.nextTopIP(w.rn)
	}
	log.Infof("Successfully reloaded weight file %s", w.fileName)
	return nil
}

// Read the weight file
func (w *weightedRR) readWeightFile() (bool, error) {
	reader, err := os.Open(filepath.Clean(w.fileName))
	if err != nil {
		return false, errOpen
	}
	defer reader.Close()

	// check if the contents has changed
	var buf bytes.Buffer
	tee := io.TeeReader(reader, &buf)
	bytes, err := io.ReadAll(tee)
	if err != nil {
		return false, err
	}
	md5sum := md5.Sum(bytes)
	if md5sum == w.md5sum {
		// file contents has not changed
		return false, nil
	}
	w.md5sum = md5sum
	scanner := bufio.NewScanner(&buf)

	// Parse the weight file contents
	return true, w.parseWeights(scanner)
}

// Parse the weight file contents
func (w *weightedRR) parseWeights(scanner *bufio.Scanner) error {
	// Reset domains
	w.domains = make(map[string]*domain)

	var curd *domain
	for scanner.Scan() {
		nextLine := strings.TrimSpace(scanner.Text())
		if len(nextLine) == 0 || nextLine[0:1] == "#" {
			// Empty and comment lines are ignored
			continue
		}
		fields := strings.Fields(nextLine)
		switch len(fields) {
		case 1:
			// (domain) name
			dname := fields[0]

			// sanity check
			if net.ParseIP(dname) != nil {
				return fmt.Errorf("Wrong domain name:\"%s\" in weight file %s. (Maybe a missing weight value?)",
					dname, w.fileName)
			}

			// add the root domain if it is missing
			if dname[len(dname)-1] != '.' {
				dname += "."
			}
			var ok bool
			curd, ok = w.domains[dname]
			if !ok {
				curd = &domain{}
				w.domains[dname] = curd
			}
		case 2:
			// IP address and weight value
			ip := net.ParseIP(fields[0])
			if ip == nil {
				return fmt.Errorf("Wrong IP address:\"%s\" in weight file %s", fields[0], w.fileName)
			}
			weight, err := strconv.ParseUint(fields[1], 10, 8)
			if err != nil {
				return fmt.Errorf("Wrong weight value:\"%s\" in weight file %s", fields[1], w.fileName)
			}
			witem := &weightItem{address: ip, value: uint8(weight)}
			if curd == nil {
				return fmt.Errorf("Missing domain name in weight file %s", w.fileName)
			}
			curd.weights = append(curd.weights, witem)
		default:
			return fmt.Errorf("Could not parse weight line:\"%s\" in weight file %s", nextLine, w.fileName)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("Weight file %s parsing error:%s", w.fileName, err)
	}

	return nil
}

func (w *weightedRR) print() {
	fmt.Printf("weightedRR --- fname:%s reload:%v ", w.fileName, w.reload)
	for k, d := range w.domains {
		fmt.Printf("domain:%s wsum:%v ", k, d.wsum)
		fmt.Printf("weights:[")
		for _, i := range d.weights {
			fmt.Printf("%+v, ", *i)
		}
	}
	fmt.Printf("]\n")
}

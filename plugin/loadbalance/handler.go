// Package loadbalance is a plugin for rewriting responses to do "load balancing"
package loadbalance

import (
	"context"
	"crypto/md5"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"

	"github.com/miekg/dns"
)

type (
	// RoundRobin is a plugin to rewrite responses for "load balancing".
	RoundRobin struct {
		Next    plugin.Handler
		policy  string
		weights *weightedRR
	}
	// "weighted-round-robin" policy specific data
	weightedRR struct {
		fileName string
		reload   time.Duration
		md5sum   [md5.Size]byte
		domains  map[string]*domain
		isRandom bool
		rn       *rand.Rand
		mutex    sync.Mutex
	}
	// Per domain weights and the expected top entry in the result list
	domain struct {
		weights []*weightItem
		topIP   net.IP
		topIPupdater
	}
	// Weight assigned to an address
	weightItem struct {
		address net.IP
		value   uint8
	}
	// Get the expected top IP for the next answer
	topIPupdater interface {
		nextTopIP(curd *domain, rn *rand.Rand)
	}
)

// ServeDNS implements the plugin.Handler interface.
func (rr RoundRobin) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	rrw := &RoundRobinResponseWriter{ResponseWriter: w, policy: rr.policy, weights: rr.weights}
	return plugin.NextOrFailure(rr.Name(), rr.Next, ctx, rrw, r)
}

// Name implements the Handler interface.
func (rr RoundRobin) Name() string { return "loadbalance" }

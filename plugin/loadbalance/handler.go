// Package loadbalance is a plugin for rewriting responses to do "load balancing"
package loadbalance

import (
	"context"

	"github.com/coredns/coredns/plugin"

	"github.com/miekg/dns"
)

// RoundRobin is a plugin to rewrite responses for "load balancing".
type LoadBalance struct {
	Next    plugin.Handler
	shuffle func(*dns.Msg) *dns.Msg
}

// ServeDNS implements the plugin.Handler interface.
func (rr LoadBalance) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	rrw := &LoadBalanceResponseWriter{ResponseWriter: w, shuffle: rr.shuffle}
	return plugin.NextOrFailure(rr.Name(), rr.Next, ctx, rrw, r)
}

// Name implements the Handler interface.
func (rr LoadBalance) Name() string { return "loadbalance" }

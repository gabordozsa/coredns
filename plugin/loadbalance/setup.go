package loadbalance

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
)

var log = clog.NewWithPlugin("loadbalance")
var errOpen = errors.New("Weight file open error")

func init() { plugin.Register("loadbalance", setup) }

func setup(c *caddy.Controller) error {
	policy, weighted, err := parse(c)
	if err != nil {
		return plugin.Error("loadbalance", err)
	}

	if policy == weightedRoundRobinPolicy {
		weighted.randInit()

		stopReloadChan := make(chan bool)
		weighted.periodicWeightUpdate(stopReloadChan)

		c.OnStartup(func() error {
			err := weighted.updateWeights()
			if errors.Is(err, errOpen) && weighted.reload != 0 {
				log.Warningf("Failed to open weight file:%v. Will try again in %v",
					err, weighted.reload)
			} else if err != nil {
				return plugin.Error("loadbalance", err)
			}
			return nil
		})
		c.OnShutdown(func() error {
			close(stopReloadChan)
			return nil
		})
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return RoundRobin{Next: next, policy: policy, weights: weighted}
	})

	return nil
}

func parse(c *caddy.Controller) (string, *weightedRR, error) {
	config := dnsserver.GetConfig(c)

	for c.Next() {
		args := c.RemainingArgs()
		if len(args) == 0 {
			return ramdomShufflePolicy, nil, nil
		}
		switch args[0] {
		case ramdomShufflePolicy:
			if len(args) > 1 {
				return "", nil, c.Errf("unknown property for %s", args[0])
			}
			return ramdomShufflePolicy, nil, nil
		case weightedRoundRobinPolicy:
			if len(args) < 2 {
				return "", nil, c.Err("missing weight file argument")
			}

			if len(args) > 2 {
				return "", nil, c.Err("unexpected argument(s)")
			}

			w := &weightedRR{
				reload:    30 * time.Second,
				randomGen: &randomUint{},
			}

			fileName := args[1]
			if !filepath.IsAbs(fileName) && config.Root != "" {
				fileName = filepath.Join(config.Root, fileName)
			}
			w.fileName = fileName

			for c.NextBlock() {
				switch c.Val() {
				case "reload":
					t := c.RemainingArgs()
					if len(t) < 1 {
						return "", nil, c.Err("reload duration value is missing")
					}
					if len(t) > 1 {
						return "", nil, c.Err("unexpected argument")
					}
					d, err := time.ParseDuration(t[0])
					if err != nil {
						return "", nil, c.Errf("invalid reload duration '%s'", t[0])
					}
					w.reload = d
				default:
					return "", nil, c.Errf("unknown property '%s'", c.Val())
				}
			}
			return weightedRoundRobinPolicy, w, nil
		default:
			return "", nil, fmt.Errorf("unknown policy: %s", args[0])
		}
	}
	return "", nil, c.ArgErr()
}

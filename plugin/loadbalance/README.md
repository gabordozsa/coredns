# loadbalance

## Name

*loadbalance* - randomizes the order of A, AAAA and MX records.

## Description

The *loadbalance* will act as a round-robin DNS load balancer by randomizing the order of A, AAAA,
and MX records in the answer.

See [Wikipedia](https://en.wikipedia.org/wiki/Round-robin_DNS) about the pros and cons of this
setup. It will take care to sort any CNAMEs before any address records, because some stub resolver
implementations (like glibc) are particular about that.

## Syntax

~~~
loadbalance [POLICY]
~~~

* **POLICY** is how to balance. Available options are "round_robin" and "weighted_round_robin". The default is "round_robin".

The "round_robin" strategy randomizes the order of  A, AAAA, and MX records applying a uniform probability distribution.

The "weighted_round_robin" policy assigns weight values to IPs to control the relative likelihood of particular IPs to be returned as top
entry (A or AAAA record) in the answer. See [Wikipedia](https://en.wikipedia.org/wiki/Weighted_round_robin) about a generic
description of weighted round robin load balancing strategy.

Additional option required by "weighted_round_robin" policy:

~~~
loadbalance weighted_round_robin WEIGHTFILE
~~~

* **WEIGHTFILE** the weight file containing the weight values assigned to IPs. If the path is relative, the path from the **root** plugin will be prepended to it.

The weight file is parsed line-by-line. If the first character in the line is '#' then the line is ignored (i.e. comment line). Otherwise, if the line contains a single word then it is a domain name. If the line contains two words then the first one is an IP and the second one is a weight value for that IP. The IPs are addresses for the domain name which is defined above them in the file. Multiple domain names can be specified in the same weight file. A weight value must be in the range [1,255].

If the server receives a query for which no domain name is specified in the corresponding weight file then the answer is returned unmodified. If the domain name is specified in the weight file but the answer does not include the expected top IP (or there isn't any IP/weight pair specified for the particular domain name) then the answer is returned unmodified.

More (optional) control for the "weighted_round_robin" policy:

~~~
loadbalance weighted_round_robin WEIGHTFILE {
			reload DURATION
			[deterministic]
}
~~~

* **reload** the interval to reload **WEIGHTFILE** and update weight assignments if there are changes in the file. The default value is **30s**. A value of **0s** means to not scan for changes and reload.

* **deterministic** switch to deterministic weighted-round-robin strategy. This remove randomness from answer re-ordering. A weight value defines exactly how many
times a particular IP should be in the top record for consecutive queries to the same domain name.


## Examples

Load balance replies coming back from Google Public DNS:

~~~ corefile
. {
    loadbalance round_robin
    forward . 8.8.8.8 8.8.4.4
}
~~~

Use weighted round robin strategy to load balance replies defined by the **file** plugin:

~~~ corefile
demo.plmt {
        file ./demo.plmt {
                reload 10s
        }
        loadbalance weighted_round_robin ./demo.plt.weights {
                    reload 10s
        }
}
~~~

where the weight file **./demo.plt.weights** contains:

~~~
wwww.demo.plmt
100.64.1.1 3
100.64.1.2 1
100.64.1.3 2
~~~

This assigns weight vales **3**, **1** and **2** to the IPs **100.64.1.1**, **100.64.1.2** and **100.64.1.3**, respectively. These IPs are addresses in A records for the domain name "wwww.demo.plmt". (The IPs for the A records are defined in the ./demo.plmt file using the **file** plugin in this example).

In this example, the ratio between the number of answers in which **100.64.1.1**, **100.64.1.2** or **100.64.1.3** is in the top A record should converge to  **3 : 1 : 2**.  (E.g. there should be twice as many answers with **100.64.1.3** in the top A record than with **100.64.1.2**).

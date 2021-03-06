package reacter

//go:generate esc -o static.go -pkg reacter -modtime 1500000000 -prefix ui ui

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ghetzel/diecast"
	"github.com/ghetzel/go-stockutil/httputil"
	"github.com/ghetzel/go-stockutil/log"
	"github.com/ghetzel/go-stockutil/maputil"
	"github.com/ghetzel/go-stockutil/netutil"
	"github.com/ghetzel/go-stockutil/stringutil"
	"github.com/ghetzel/go-stockutil/typeutil"
	"github.com/husobee/vestigo"
	"github.com/urfave/negroni"
)

var ZeroconfInstanceName = `reacter`

type Server struct {
	ZeroconfMDNS     bool
	ZeroconfEC2Tag   string
	PathPrefix       string
	reacter          *Reacter
	ec2CheckInterval time.Duration
}

func NewServer(reacter *Reacter) *Server {
	return &Server{
		reacter:          reacter,
		ec2CheckInterval: 60 * time.Second,
	}
}

func (self *Server) ListenAndServe(address string) error {
	server := negroni.New()
	router := vestigo.NewRouter()
	ui := diecast.NewServer(func() interface{} {
		if dir := os.Getenv(`UI`); dir != `` {
			return dir
		} else {
			return FS(false)
		}
	}())

	ui.RoutePrefix = strings.TrimSuffix(self.PathPrefix, `/`)

	router.Get(`/reacter/v1/node`, func(w http.ResponseWriter, req *http.Request) {
		httputil.RespondJSON(w, self.reacter)
	})

	router.Get(`/reacter/v1/checks`, func(w http.ResponseWriter, req *http.Request) {
		httputil.RespondJSON(w, maputil.M(&self.reacter.checkset).MapNative())
	})

	vestigo.CustomNotFoundHandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ui.ServeHTTP(w, req)
	})

	if self.ZeroconfMDNS || self.ZeroconfEC2Tag != `` {
		_, portS, _ := net.SplitHostPort(address)
		go self.startZeroconf(int(typeutil.Int(portS)))
	} else {
		self.reacter.Peers = []*netutil.Service{
			self.localNode(address),
		}
	}

	server.UseHandler(router)
	server.Run(address)
	return nil
}

func (self *Server) startZeroconf(port int) {
	var ec2lastChecked time.Time

	if self.ZeroconfMDNS || self.ZeroconfEC2Tag != `` {
		if self.ZeroconfMDNS {
			// register ourselves
			if _, err := netutil.ZeroconfRegister(&netutil.Service{
				Instance: fmt.Sprintf("%s-%s-%d", ZeroconfInstanceName, self.reacter.NodeName, os.Getpid()),
				Service:  `_http._tcp`,
				Domain:   `.local`,
				Port:     port,
			}); err != nil {
				log.Warningf("[zeroconf] failed to register: %v", err)
				return
			}
		}

		for {
			peers := make([]*netutil.Service, 0)
			ran := false

			// perform AWS EC2 discovery
			if tag := self.ZeroconfEC2Tag; tag != `` {
				if ec2lastChecked.IsZero() || time.Since(ec2lastChecked) > self.ec2CheckInterval {
					ec2lastChecked = time.Now()
					ran = true
					tagName, tagValue := stringutil.SplitPair(tag, `=`)

					if ec2svc, err := DiscoverEC2ByTag(tagName, strings.Split(tagValue, `,`)...); err == nil {
						log.Debugf("[zeroconf] EC2 discovery: %v=%v", tagName, tagValue)

						for i, _ := range ec2svc {
							ec2svc[i].Port = port

							if !strings.Contains(ec2svc[i].Address, `:`) {
								ec2svc[i].Address = fmt.Sprintf("%s:%d", ec2svc[i].Address, port)
							}
						}

						peers = append(peers, ec2svc...)
					} else {
						log.Warningf("[zeroconf] EC2 discovery: %v", err)
					}
				} else {
					time.Sleep(time.Second)
				}
			}

			if self.ZeroconfMDNS {
				// perform mDNS discovery
				if err := netutil.ZeroconfDiscover(&netutil.ZeroconfOptions{
					Timeout:       10 * time.Second,
					MatchInstance: `^` + ZeroconfInstanceName + `-`,
				}, func(svc *netutil.Service) bool {
					log.Debugf("[zeroconf] found peer: %v", svc)

					ran = true
					peers = append(peers, svc)
					self.reacter.Peers = peers

					return true
				}); err != nil {
					log.Warningf("[zeroconf] discovery: %v", err)
				}
			}

			if ran {
				self.reacter.Peers = peers
			}
		}
	}
}

func (self *Server) localNode(address string) *netutil.Service {
	hostname, _ := os.Hostname()
	addrs, _ := netutil.RoutableAddresses()

	if address == `` {
		address = netutil.DefaultAddress().IP.String()
	}

	svc := &netutil.Service{
		Hostname: hostname,
		Address:  address,
	}

	for _, addr := range addrs {
		svc.Addresses = append(svc.Addresses, addr.IP)
	}

	return svc
}

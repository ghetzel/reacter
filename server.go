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
	ZeroconfMDNS   bool
	ZeroconfEC2Tag string
	reacter        *Reacter
	PathPrefix     string
}

func NewServer(reacter *Reacter) *Server {
	return &Server{
		reacter: reacter,
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
	}

	server.UseHandler(router)
	server.Run(address)
	return nil
}

func (self *Server) startZeroconf(port int) {
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

			// perform AWS EC2 discovery
			if tag := self.ZeroconfEC2Tag; tag != `` {
				tagName, tagValue := stringutil.SplitPair(tag, `=`)

				if ec2svc, err := DiscoverEC2ByTag(tagName, strings.Split(tagValue, `,`)...); err == nil {
					for i, _ := range ec2svc {
						ec2svc[i].Port = port
					}

					peers = append(peers, ec2svc...)
				} else {
					log.Warningf("[zeroconf] EC2 discovery: %v", err)
				}
			}

			if self.ZeroconfMDNS {
				// perform mDNS discovery
				if err := netutil.ZeroconfDiscover(&netutil.ZeroconfOptions{
					Timeout:       10 * time.Second,
					MatchInstance: `^` + ZeroconfInstanceName + `-`,
				}, func(svc *netutil.Service) bool {
					log.Debugf("[zeroconf] found peer: %v", svc)

					peers = append(peers, svc)
					self.reacter.Peers = peers

					return true
				}); err != nil {
					log.Warningf("[zeroconf] discovery: %v", err)
				}
			}

			self.reacter.Peers = peers
		}
	}
}

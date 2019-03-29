package reacter

//go:generate esc -o static.go -pkg reacter -modtime 1500000000 -prefix ui ui

import (
	"net/http"
	"os"

	"github.com/ghetzel/diecast"
	"github.com/ghetzel/go-stockutil/httputil"
	"github.com/ghetzel/go-stockutil/maputil"
	"github.com/husobee/vestigo"
	"github.com/urfave/negroni"
)

type Server struct {
	reacter *Reacter
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

	router.Get(`/reacter/v1/checks`, func(w http.ResponseWriter, req *http.Request) {
		httputil.RespondJSON(w, maputil.M(&self.reacter.checkset).MapNative())
	})

	vestigo.CustomNotFoundHandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ui.ServeHTTP(w, req)
	})

	server.UseHandler(router)
	server.Run(address)
	return nil
}

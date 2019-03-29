package reacter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/ghetzel/go-stockutil/executil"
	"github.com/ghetzel/go-stockutil/log"
	"github.com/ghetzel/reacter/util"
	"github.com/ghodss/yaml"
)

var DefaultCacheDir = executil.RootOrString(
	`/dev/shm/reacter/handler-queries`,
	`~/.cache/reacter`,
)

type EventRouter struct {
	NodeName   string
	Handlers   []*Handler
	ConfigFile string
	ConfigDir  string
	CacheDir   string
}

type HandlerConfig struct {
	HandlerDefinitions []Handler `json:"handlers"`
}

func NewEventRouter() *EventRouter {
	return &EventRouter{
		CacheDir: DefaultCacheDir,
	}
}

func (self *EventRouter) AddHandler(handler *Handler) error {
	//  load cache data
	handler.LoadNodeFile()

	self.Handlers = append(self.Handlers, handler)
	return nil
}

// Loads the ConfigFile (if present), and recursively scans and load all *.yml files in ConfigDir.
func (self *EventRouter) ReloadConfig() error {
	return util.LoadConfigFiles(self.ConfigFile, self.ConfigDir, self)
}

func (self *EventRouter) LoadConfig(path string) error {
	if file, err := os.Open(path); err == nil {
		if data, err := ioutil.ReadAll(file); err == nil {
			handlerConfigs := HandlerConfig{}

			if err := yaml.Unmarshal(data, &handlerConfigs); err == nil {
				for _, handler := range handlerConfigs.HandlerDefinitions {
					if err := self.AddHandler(&handler); err != nil {
						log.Errorf("Error adding handler '%s': %v", handler.Name, err)
					}
				}
			} else {
				return err
			}
		} else {
			return err
		}
	} else {
		return err
	}

	return nil
}

func (self *EventRouter) RunQueryCacher(interval time.Duration) error {
	if err := os.MkdirAll(self.CacheDir, 0755); err != nil {
		return err
	}

	self.RegenerateCache()

	if interval > 0 {
		log.Infof("Starting query cache refresh every %s", interval)
		ticker := time.NewTicker(interval)
		for range ticker.C {
			self.RegenerateCache()
		}
	}

	return nil
}

func (self *EventRouter) RegenerateCache() {
	for _, handler := range self.Handlers {
		go func(h *Handler) {
			if nodes, err := h.ExecuteNodeQuery(); err == nil {
				if len(nodes) > 0 {
					data := strings.Join(nodes, "\n") + "\n"
					filename := h.GetCacheFilename()

					log.Debugf("Caching output of query for handler '%s'", h.Name)
					if err := ioutil.WriteFile(filename, []byte(data[:]), 0644); err != nil {
						log.Errorf("Failed to write cache file '%s': %v", filename, err)
					}
				}
			} else {
				log.Warningf("Query command for handler '%s' failed: %v", h.Name, err)
			}
		}(handler)
	}
}

func (self *EventRouter) Run(input io.Reader) error {
	if err := self.ReloadConfig(); err == nil {
		log.Infof("%d handler(s) registered", len(self.Handlers))

		if len(self.Handlers) > 0 {
			inputScanner := bufio.NewScanner(input)
			hasErrored := false

			//  for each line of input...
			for inputScanner.Scan() {
				line := inputScanner.Text()
				done := make(chan bool)

				go func() {
					var check CheckEvent

					//  load the input line and execute all matching handlers
					if err := json.Unmarshal([]byte(line[:]), &check); err == nil && check.Check != nil {
						for _, handler := range self.Handlers {
							//  check if we should execute then do so
							if handler.ShouldExec(check.Check) {
								if err := handler.Execute(check); err != nil {
									log.Errorf("Error executing handler %s: %v", handler.Name, err)
									hasErrored = true
								} else {
									log.Infof("Executed handler '%s' for check %s/%s", handler.Name, check.Check.NodeName, check.Check.Name)
								}

								handler.lastFiredAt = time.Now()
							}
						}
					} else {
						log.Warningf("Failed to parse input line: %v", err)
					}

					done <- true
				}()

				select {
				case <-done:
				}
			}

			if hasErrored {
				return fmt.Errorf("Encountered one or more errors during handler execution")
			}
		} else {
			log.Infof("No handlers defined, nothing to do")
		}

		return nil
	} else {
		return err
	}
}

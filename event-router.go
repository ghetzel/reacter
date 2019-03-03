package reacter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ghetzel/go-stockutil/log"
	"github.com/ghodss/yaml"
)

const (
	DEFAULT_CACHE_DIR = `/dev/shm/reacter/handler-queries`
)

type EventRouter struct {
	NodeName  string
	Handlers  []*Handler
	ConfigDir string
	CacheDir  string
}

type HandlerConfig struct {
	HandlerDefinitions []Handler `json:"handlers"`
}

func NewEventRouter() *EventRouter {
	rv := new(EventRouter)
	rv.Handlers = make([]*Handler, 0)
	rv.CacheDir = DEFAULT_CACHE_DIR

	return rv
}

func (self *EventRouter) AddHandler(handlerConfig Handler) error {
	handler := NewHandler()

	if handlerConfig.Directory != `` {
		if info, err := os.Stat(handlerConfig.Directory); err == nil {
			if info.IsDir() {
				handler.Directory = handlerConfig.Directory
			} else {
				return fmt.Errorf("'%s' is not a directory", handlerConfig.Directory)
			}
		} else {
			return err
		}
	}

	handler.QueryCommand = handlerConfig.QueryCommand
	handler.Name = handlerConfig.Name
	handler.Command = handlerConfig.Command
	handler.Environment = handlerConfig.Environment
	handler.Parameters = handlerConfig.Parameters
	handler.NodeFile = handlerConfig.NodeFile
	handler.NodeFileAutoreload = handlerConfig.NodeFileAutoreload
	handler.CacheDir = self.CacheDir

	if handlerConfig.Timeout > 0 {
		handler.Timeout = handlerConfig.Timeout
	}

	if handlerConfig.QueryTimeout > 0 {
		handler.QueryTimeout = handlerConfig.QueryTimeout
	}

	if len(handlerConfig.CheckNames) > 0 {
		handler.CheckNames = handlerConfig.CheckNames
	}

	if len(handlerConfig.States) > 0 {
		handler.States = handlerConfig.States
	}

	//  load cache data
	handler.LoadNodeFile()

	self.Handlers = append(self.Handlers, handler)
	return nil
}

func (self *EventRouter) LoadConfigDir(path string) error {
	self.ConfigDir = path

	log.Debugf("Loading handler configuration from %s", self.ConfigDir)

	filepath.Walk(self.ConfigDir, func(path string, info os.FileInfo, err error) error {
		if strings.HasSuffix(path, `.yml`) && info.Mode().IsRegular() {
			log.Debugf("Loading: %s", path)
			if err := self.LoadConfig(path); err != nil {
				log.Errorf("Error loading %s: %v", path, err)
				return err
			}
		}

		return nil
	})

	return nil
}

func (self *EventRouter) LoadConfig(path string) error {
	if file, err := os.Open(path); err == nil {
		if data, err := ioutil.ReadAll(file); err == nil {
			handlerConfigs := HandlerConfig{}
			if err := yaml.Unmarshal(data, &handlerConfigs); err == nil {
				for _, handlerConfig := range handlerConfigs.HandlerDefinitions {
					if err := self.AddHandler(handlerConfig); err != nil {
						log.Errorf("Error adding handler '%s': %v", handlerConfig.Name, err)
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
	log.Debugf("Handling check events read from standard input")

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
		return fmt.Errorf("No handlers defined, nothing to do")
	}

	return nil
}

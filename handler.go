package reacter

import (
	"fmt"
	"io"
	"io/ioutil"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ghetzel/go-stockutil/fileutil"
	"github.com/ghetzel/go-stockutil/log"
)

var DefaultHandleExecTimeout = 6 * time.Second
var DefaultHandleQueryExecTimeout = 3 * time.Second

type Handler struct {
	Name               string            `json:"name"`
	QueryCommand       []string          `json:"query,omitempty"`
	NodeFile           string            `json:"nodefile,omitempty"`
	NodeFileAutoreload bool              `json:"nodefile_autoreload,omitempty"`
	NodeNames          []string          `json:"node_names,omitempty"`
	SkipOK             bool              `json:"skip_ok"`
	CheckNames         []string          `json:"checks,omitempty"`
	States             []int             `json:"states,omitempty"`
	SkipFlapping       bool              `json:"skip_flapping"`
	OnlyChanges        bool              `json:"only_changes"`
	Command            []string          `json:"command,omitempty"`
	Environment        map[string]string `json:"environment,omitempty"`
	Parameters         map[string]string `json:"parameters,omitempty"`
	Directory          string            `json:"directory,omitempty"`
	Disable            bool              `json:"disable,omitempty"`
	Timeout            interface{}       `json:"timeout,omitempty"`
	Cooldown           interface{}       `json:"cooldown,omitempty"`
	QueryTimeout       interface{}       `json:"query_timeout,omitempty"`
	CacheDir           string            `json:"-"`
	lastFiredAt        time.Time
}

func (self *Handler) GetCacheFilename() string {
	return path.Join(self.CacheDir, self.Name+`.txt`)
}

func (self *Handler) ExecuteNodeQuery() ([]string, error) {
	rv := make([]string, 0)

	//  if a QueryCommand was specified, execute it first to populate node names
	if len(self.QueryCommand) > 0 {
		status := make(chan bool)

		go func() {
			log.Debugf("Executing query command: %v", self.QueryCommand)

			//  execute query command
			if nodes, err := exec.Command(self.QueryCommand[0], self.QueryCommand[1:]...).Output(); err == nil {
				lines := strings.Split(string(nodes[:]), "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if len(line) > 0 && !strings.HasPrefix(line, `#`) {
						rv = append(rv, line)
					}
				}

				log.Debugf("Query command returned %d nodes", len(rv))
			} else {
				log.Debugf("Skipping handler '%s' because the query command failed: %v", self.Name, err)
				status <- false
				return
			}

			status <- true
		}()

		//  wait for the query command to complete or the QueryTimeout, whichever comes first
		select {
		case s := <-status:
			if !s {
				return rv, fmt.Errorf("Query command failed")
			}
		case <-time.After(duration(self.QueryTimeout, DefaultHandleQueryExecTimeout)):
			log.Warningf("Handler '%s' timed out after %v waiting for the query command to execute", self.Name, duration(self.QueryTimeout))
		}

		//  a query command that returns no nodes means we don't handle this event
		if len(rv) == 0 {
			return rv, fmt.Errorf("Skipping handler '%s' because the query command returned no nodes", self.Name)
		}
	}

	return rv, nil
}

func (self *Handler) ShouldExec(check *Check) bool {
	//  if we're disabled, don't execute
	if self.Disable {
		return false
	}

	if cooldown := duration(self.Cooldown); cooldown > 0 {
		if !self.lastFiredAt.IsZero() {
			if since := time.Since(self.lastFiredAt); since < cooldown {
				log.Debugf("Skipping handler '%s' because it is in a cooldown period (%v < %v)", self.Name, since, cooldown)
				return false
			}
		}
	}

	//  check if we should handle this check if it's flapping
	if self.SkipFlapping && check.IsFlapping() {
		log.Debugf("Skipping handler '%s' because it doesn't handle flapping but this check is flapping", self.Name)
		return false
	}

	//  check if we should handle this check only when its state changes
	if self.OnlyChanges && !check.StateChanged {
		log.Debugf("Skipping handler '%s' because it only handles state changes and this check has not changed", self.Name)
		return false
	}

	// check if the observation is in an OK state, but we're only supposed to fire on non-OK states
	if self.SkipOK && check.IsOK() {
		log.Debugf("Skipping handler '%s' because the check is okay", self.Name)
		return false
	}

	//  check if we're supposed to re-read the NodeFile each time, and if so do it
	if self.NodeFileAutoreload {
		self.LoadNodeFile()
	}

	//  only execute the query command now if we didn't name a cachefile to load the output of
	//  said command from
	//
	//  the cachefile feature exists to avoid having to execute the query for EVERY
	//  event we process, instead relying on an external process to populate the data
	//
	if len(self.NodeFile) == 0 {
		if nodes, err := self.ExecuteNodeQuery(); err == nil {
			self.NodeNames = nodes
		} else {
			log.Warningf("%v", err)
		}
	}

	//  check if we should handle this check's node
	if len(self.NodeNames) > 0 {
		var idMatched bool
		for _, name := range self.NodeNames {
			if name == check.NodeName {
				idMatched = true
				break
			}
		}
		if !idMatched {
			log.Debugf("Skipping handler '%s' because node '%s' is not in the list of nodes to handle", self.Name, check.NodeName)
			return false
		}
	}

	//  check if we should handle this check's name
	var checkMatched bool

	if len(self.CheckNames) > 0 {
		for _, checkName := range self.CheckNames {
			if checkName == check.Name {
				checkMatched = true
				break
			}
		}
	} else {
		checkMatched = true
	}

	if !checkMatched {
		log.Debugf("Skipping handler '%s' because check '%s' is not in the list of checks to handle", self.Name, check.Name)
		return false
	}

	//  TODO: check if we should handle this check's state
	//

	//  we're here, we should execute now
	return true
}

func (self *Handler) Execute(event CheckEvent) error {
	if !self.Disable {
		if len(self.Command) > 0 {
			done := make(chan bool)

			go func() {
				log.Debugf("Executing handler '%s': %s", self.Name, self.Command)
				cmd := exec.Command(self.Command[0], self.Command[1:]...)

				//  setup working directory
				self.Directory = fileutil.MustExpandUser(self.Directory)

				if fileutil.DirExists(self.Directory) {
					cmd.Dir = self.Directory
				}

				//  pass in environment variables
				for k, v := range self.Environment {
					//  cannot set environment variables that start with "REACTER_"
					if !strings.HasPrefix(strings.ToUpper(k), `REACTER_`) {
						cmd.Env = append(cmd.Env, k+`=`+v)
					}
				}

				//  make parameters available as environment variables with predictable names
				for k, v := range self.Parameters {
					cmd.Env = append(cmd.Env, `REACTER_PARAM_`+strings.ToUpper(k)+`=`+v)
				}

				//  set well-known environment variables
				//  -------------------------------------------------------------
				if event.Check.StateChanged {
					cmd.Env = append(cmd.Env, `REACTER_STATE_CHANGED=1`)
				} else {
					cmd.Env = append(cmd.Env, `REACTER_STATE_CHANGED=0`)
				}

				if event.Check.IsFlapping() {
					cmd.Env = append(cmd.Env, `REACTER_STATE_FLAPPING=1`)
				} else {
					cmd.Env = append(cmd.Env, `REACTER_STATE_FLAPPING=0`)
				}

				if event.Check.HardState {
					cmd.Env = append(cmd.Env, `REACTER_STATE_HARD=1`)
				} else {
					cmd.Env = append(cmd.Env, `REACTER_STATE_HARD=0`)
				}

				cmd.Env = append(cmd.Env, `REACTER_STATE=`+event.Check.StateString())
				cmd.Env = append(cmd.Env, `REACTER_STATE_ID=`+strconv.Itoa(int(event.Check.State)))
				cmd.Env = append(cmd.Env, `REACTER_CHECK_ID=`+event.Check.ID())
				cmd.Env = append(cmd.Env, `REACTER_CHECK_NODE=`+event.Check.NodeName)
				cmd.Env = append(cmd.Env, `REACTER_CHECK_NAME=`+event.Check.Name)
				cmd.Env = append(cmd.Env, `REACTER_EPOCH=`+strconv.Itoa(int(event.Timestamp.Unix())))
				cmd.Env = append(cmd.Env, `REACTER_EPOCH_MS=`+strconv.Itoa(int(event.Timestamp.UnixNano())/1000000))
				cmd.Env = append(cmd.Env, `REACTER_HANDLER=`+self.Name)

				//  -------------------------------------------------------------

				//  setup STDIN pipe and write check event data to it
				if stdin, err := cmd.StdinPipe(); err == nil {

					//  start command and write raw data to its standar input
					if err := cmd.Start(); err == nil {
						io.WriteString(stdin, event.Output)
						stdin.Close()
					} else {
						log.Errorf("Handler '%s' failed to execute: %v", self.Name, err)
					}

					//  block until command exits
					if err := cmd.Wait(); err == nil {
						log.Debugf("Handler '%s' executed successfully", self.Name)
					} else {
						log.Errorf("Handler '%s' failed during execution: %v", self.Name, err)
					}
				} else {
					log.Errorf("Handler '%s' failed setting up command: %v", self.Name, err)
				}

				done <- true
			}()

			//  wait for the command to complete or the Timeout, whichever comes first
			select {
			case <-done:
				log.Debugf("Handler '%s' execution complete", self.Name)
			case <-time.After(duration(self.Timeout, DefaultHandleExecTimeout)):
				return fmt.Errorf("Handler '%s' timed out after %v waiting for the handler command to execute", self.Name, duration(self.Timeout))
			}
		} else {
			self.Disable = true
			return fmt.Errorf("Cannot execute handler '%s': command not specified; disabling handler", self.Name)
		}
	}

	return nil
}

func (self *Handler) LoadNodeFile() {
	if len(self.NodeFile) > 0 {
		//  if the value "true" is passed via the YAML, use the default cache file location
		if self.NodeFile == `true` {
			self.NodeFile = self.GetCacheFilename()
		}

		log.Debugf("Loading nodes from nodefile at '%s'", self.NodeFile)

		if data, err := ioutil.ReadFile(self.NodeFile); err == nil {
			self.NodeNames = nil

			for _, line := range strings.Split(string(data[:]), "\n") {
				line = strings.TrimSpace(line)
				if len(line) > 0 {
					self.NodeNames = append(self.NodeNames, line)
				}
			}

			log.Debugf("Node file contained %d nodes", len(self.NodeNames))
		}
	}
}

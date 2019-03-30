package reacter

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/ghetzel/go-stockutil/log"
	"github.com/ghetzel/go-stockutil/sliceutil"
	"github.com/ghetzel/go-stockutil/typeutil"
	shellwords "github.com/mattn/go-shellwords"
)

var DefaultCheckInterval = 60
var DefaultCheckTimeout = 10000

type Check struct {
	UID               string                 `json:"id"`
	NodeName          string                 `json:"node_name"`
	Name              string                 `json:"name"`
	Command           interface{}            `json:"command"`
	Timeout           interface{}            `json:"timeout"`
	Enabled           bool                   `json:"enabled"`
	State             ObservationState       `json:"state"`
	HardState         bool                   `json:"hard"`
	StateChanged      bool                   `json:"changed"`
	Parameters        map[string]interface{} `json:"parameters"`
	Environment       map[string]string      `json:"environment"`
	Directory         string                 `json:"directory,omitempty"`
	Interval          interface{}            `json:"interval"`
	FlapThresholdHigh float64                `json:"flap_threshold_high"`
	FlapThresholdLow  float64                `json:"flap_threshold_low"`
	Rise              int                    `json:"rise"`
	Fall              int                    `json:"fall"`
	Observations      *Observations          `json:"observations"`
	EventStream       chan CheckEvent        `json:"-"`
	StopMonitorC      chan bool              `json:"-"`
}

type CheckEvent struct {
	Check       *Check       `json:"check"`
	Observation *Observation `json:"observation,omitempty"`
	Output      string       `json:"output,omitempty"`
	Error       bool         `json:"error,omitempty"`
	Timestamp   time.Time    `json:"timestamp"`
}

func NewCheck() *Check {
	return &Check{
		Observations: NewObservations(),
		Timeout:      DefaultCheckTimeout,
		Enabled:      true,
		HardState:    true,
		State:        SuccessState,
		StateChanged: true,
		Rise:         1,
		Fall:         1,
		Parameters:   make(map[string]interface{}),
		Environment:  make(map[string]string),
		Interval:     DefaultCheckInterval,
		StopMonitorC: make(chan bool),
	}
}

func (self *Check) IsFlapping() bool {
	return self.Observations.Flapping
}

func (self *Check) StateString() string {
	state := `unknown`

	switch self.State {
	case SuccessState:
		state = `okay`
	case WarningState:
		state = `warning`
	case CriticalState:
		state = `critical`
	}

	return state
}

func (self *Check) IsOK() bool {
	return (self.State == SuccessState)
}

func (self *Check) ID() string {
	idStr := fmt.Sprintf("%s:%d:%s", self.NodeName, os.Getpid(), self.Name)
	hash := sha1.Sum([]byte(idStr[:]))
	return hex.EncodeToString([]byte(hash[:]))
}

func (self *Check) cmdline() ([]string, error) {
	if typeutil.IsEmpty(self.Command) {
		return nil, fmt.Errorf("command not specified")
	} else if typeutil.IsArray(self.Command) {
		if args := sliceutil.Stringify(self.Command); len(args) > 0 {
			return args, nil
		} else {
			return nil, fmt.Errorf("command not specified")
		}
	} else if args, err := shellwords.Parse(typeutil.String(self.Command)); err == nil {
		return args, nil
	} else {
		return nil, err
	}
}

func (self *Check) Execute() (Observation, error) {
	self.UID = self.ID()

	if self.Enabled {
		if args, err := self.cmdline(); err == nil {
			var output []byte
			var err error
			var exitStatus int

			errchan := make(chan error)

			go func() {
				var err error
				log.Debugf("Executing check '%s': %s", self.Name, args)
				cmd := exec.Command(args[0], args[1:]...)

				if self.Directory != `` {
					cmd.Dir = self.Directory
				}

				//  pass in environment variables
				for k, v := range self.Environment {
					cmd.Env = append(cmd.Env, k+`=`+v)
				}

				output, err = cmd.Output()
				errchan <- err
			}()

			//  wait for the command to complete or the Timeout, whichever comes first
			select {
			case err = <-errchan:
				log.Debugf("Check '%s' execution complete", self.Name)
			case <-time.After(duration(self.Timeout)):
				return Observation{}, fmt.Errorf("Timed out after %dms waiting for the command to execute", self.Timeout)
			}

			if err == nil {
				exitStatus = 0
			} else {
				if exiterr, ok := err.(*exec.ExitError); ok {
					if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
						exitStatus = status.ExitStatus()
					} else {
						log.Errorf("Error running check '%s': unknown exit status", self.Name)
						exitStatus = 3
					}
				} else {
					return Observation{}, fmt.Errorf("Error running check '%s': %v", self.Name, err)
				}
			}

			//  build observation
			observation := Observation{
				Timestamp:       time.Now(),
				PerformanceData: make(map[string]Measurement),
			}

			observation.SetState(exitStatus)

			//  add STDOUT lines
			outputScanner := bufio.NewScanner(bytes.NewReader(output))

			for outputScanner.Scan() {
				line := outputScanner.Text()
				parts := strings.SplitN(line, `|`, 2)

				observation.Output = append(observation.Output, strings.TrimSpace(parts[0]))

				if len(parts) > 1 {
					for _, measurement := range strings.Split(strings.TrimSpace(parts[1]), ` `) {
						kv := strings.SplitN(measurement, `=`, 2)
						if len(kv) == 2 {
							values := strings.Split(kv[1], `;`)
							if len(values) >= 5 {
								m := Measurement{}
								m.SetValues(values[0], values[1], values[2], values[3], values[4])
								observation.PerformanceData[kv[0]] = m
							}
						}
					}
				}
			}

			if err := self.Observations.Push(observation); err != nil {
				return observation, fmt.Errorf("Failed to save observation for check '%s': %v", self.Name, err)
			} else {
				//  set the current state of the check based on observation
				//  results and rise/fall parameters

				//  currently failed; check if the last observation makes us pass
				if self.Rise > 1 && self.State != SuccessState {
					if self.IsRisen() {
						self.State = SuccessState
						self.StateChanged = true
					} else {
						self.StateChanged = false
					}

					//  currently okay; check if the last observation makes us fail
				} else if self.Fall > 1 && self.State == SuccessState {
					if self.IsFallen() {
						self.State = observation.State
						self.StateChanged = true
					} else {
						self.StateChanged = false
					}

					//  rise/fall are both 1; just set check state to the latest observation state
				} else {
					self.State = observation.State

					if len(self.Observations.Values) > 1 {
						self.StateChanged = (self.Observations.Values[len(self.Observations.Values)-2].State != observation.State)
					}
				}

				return observation, nil
			}

		} else {
			self.Enabled = false
			return Observation{}, fmt.Errorf("Cannot execute check '%s': %v; disabling check", self.Name, err)
		}
	} else {
		return Observation{}, fmt.Errorf("Cannot execute check '%s': check is disabled", self.Name)
	}
}

func (self *Check) executeAndPush() {
	var event CheckEvent

	if observation, err := self.Execute(); err == nil {
		//  push event onto event channel
		event = CheckEvent{
			Timestamp:   time.Now(),
			Check:       self,
			Observation: &observation,
			Output:      strings.Join(observation.Output, "\n"),
			Error:       false,
		}
	} else {
		event = CheckEvent{
			Timestamp: time.Now(),
			Check:     self,
			Output:    err.Error(),
			Error:     true,
		}
	}

	self.EventStream <- event
}

func (self *Check) IsRisen() bool {
	oLen := len(self.Observations.Values)

	//  need to have a minimum of observations to make any determination
	if oLen >= self.Rise {
		lastN := self.Observations.Values[(oLen - self.Rise):]

		for _, observation := range lastN {
			if observation.State != SuccessState {
				return false
			}
		}
	}

	return true
}

func (self *Check) IsFallen() bool {
	oLen := len(self.Observations.Values)

	//  need to have a minimum of observations to make any determination
	if oLen >= self.Fall {
		lastN := self.Observations.Values[(oLen - self.Fall):]

		for _, observation := range lastN {
			if observation.State == SuccessState {
				return false
			}
		}

		return true
	} else {
		return false
	}
}

func (self *Check) Monitor(eventStream chan CheckEvent) error {
	ticker := time.NewTicker(duration(self.Interval))
	self.EventStream = eventStream

	self.executeAndPush()

	for {
		select {
		case <-ticker.C:
			self.executeAndPush()
		case stop := <-self.StopMonitorC:
			if stop {
				log.Infof("Check '%s' monitor is stopping", self.Name)
				return nil
			}
		}
	}
}

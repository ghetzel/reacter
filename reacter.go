package reacter

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger(`reacter`)

type Reacter struct {
	NodeName         string
	Checks           []*Check
	Events           chan CheckEvent
	ConfigDir        string
	PrintJson        bool
	OnlyPrintChanges bool
	SuppressFlapping bool
}

type Config struct {
	ChecksDefinitions []Check `json:"checks"`
}

func NewReacter() *Reacter {
	rv := new(Reacter)
	rv.Checks = make([]*Check, 0)
	rv.Events = make(chan CheckEvent)
	return rv
}

func (self *Reacter) AddCheck(checkConfig Check) error {
	check := NewCheck()

	if checkConfig.Directory != `` {
		if info, err := os.Stat(checkConfig.Directory); err == nil {
			if info.IsDir() {
				check.Directory = checkConfig.Directory
			} else {
				return fmt.Errorf("'%s' is not a directory", checkConfig.Directory)
			}
		} else {
			return err
		}
	}

	if checkConfig.Interval > 0 {
		check.Interval = checkConfig.Interval
	} else if checkConfig.Interval < 0 {
		return fmt.Errorf("Cannot specify a negative interval (%d)", checkConfig.Interval)
	}

	if checkConfig.FlapThresholdHigh > 0 {
		check.FlapThresholdHigh = checkConfig.FlapThresholdHigh
		check.Observations.FlapThresholdHigh = checkConfig.FlapThresholdHigh
	} else if checkConfig.FlapThresholdHigh < 0 {
		return fmt.Errorf("Cannot specify a negative high flap threshold (%d)", int(checkConfig.FlapThresholdHigh))
	}

	if checkConfig.FlapThresholdLow > 0 {
		check.FlapThresholdLow = checkConfig.FlapThresholdLow
		check.Observations.FlapThresholdLow = checkConfig.FlapThresholdLow
	} else if checkConfig.FlapThresholdLow < 0 {
		return fmt.Errorf("Cannot specify a negative low flap threshold (%d)", int(checkConfig.FlapThresholdLow))
	}

	check.NodeName = self.NodeName
	check.Name = checkConfig.Name
	check.Command = checkConfig.Command
	check.Environment = checkConfig.Environment
	check.Parameters = checkConfig.Parameters

	if checkConfig.Timeout > 0 {
		check.Timeout = checkConfig.Timeout
	}

	if checkConfig.Rise > 0 {
		check.Rise = checkConfig.Rise
	}

	if checkConfig.Fall > 0 {
		check.Fall = checkConfig.Fall
	}

	if check.Observations.Size < check.Fall {
		log.Warningf("Check '%s' fall threshold (%d) is larger than the number of saved observations for this check (%d), setting to %d instead", check.Name, check.Fall, check.Observations.Size, check.Observations.Size)
		check.Fall = check.Observations.Size
	}

	if check.Observations.Size < check.Rise {
		log.Warningf("Check '%s' rise threshold (%d) is larger than the number of saved observations for this check (%d), setting to %d instead", check.Name, check.Rise, check.Observations.Size, check.Observations.Size)
		check.Rise = check.Observations.Size
	}

	self.Checks = append(self.Checks, check)
	return nil
}

func (self *Reacter) LoadConfigDir(path string) error {
	self.ConfigDir = path

	log.Debugf("Loading configuration from %s", self.ConfigDir)

	filepath.Walk(self.ConfigDir, func(path string, info os.FileInfo, err error) error {
		if strings.HasSuffix(path, `.yml`) && info.Mode().IsRegular() {
			log.Infof("Loading: %s", path)
			if err := self.LoadConfig(path); err != nil {
				log.Errorf("Error loading %s: %v", path, err)
				return err
			}
		}

		return nil
	})

	return nil
}

func (self *Reacter) LoadConfig(path string) error {
	if file, err := os.Open(path); err == nil {
		if data, err := ioutil.ReadAll(file); err == nil {
			checkConfigs := Config{}
			if err := yaml.Unmarshal(data, &checkConfigs); err == nil {
				for _, checkConfig := range checkConfigs.ChecksDefinitions {
					if err := self.AddCheck(checkConfig); err != nil {
						log.Errorf("Error adding check '%s': %v", checkConfig.Name, err)
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

func (self *Reacter) StartEventProcessing() {
	for {
		select {
		case event := <-self.Events:
			var suffix string
			if event.Check.IsFlapping() {
				suffix = ` [FLAPPING]`
			}

			if !event.Error {
				switch event.Check.State {
				case SuccessState:
					log.Infof("Check '%s' passed with status %d%s: %s", event.Check.Name, event.Check.State, suffix, event.Output)
				case WarningState:
					log.Warningf("Check '%s' warning with status %d%s: %s", event.Check.Name, event.Check.State, suffix, event.Output)
				case CriticalState:
					log.Errorf("Check '%s' critical with status %d%s: %s", event.Check.Name, event.Check.State, suffix, event.Output)
				default:
					log.Infof("Check '%s' failed with unknown status %d%s: %s", event.Check.Name, event.Check.State, suffix, event.Output)
				}
			} else {
				log.Errorf("Check '%s' encountered an error during execution: %v", event.Check.Name, event.Output)

				//  put a few things into the check state becuase it failed too quickly to do that itself
				if event.Check.State != 128 {
					event.Check.StateChanged = true
				}

				event.Check.State = 128
			}

			//  serialize check and print as JSON
			if self.PrintJson {
				//  ...either always, or only when the state has changed from its previous value
				if !self.OnlyPrintChanges || event.Check.StateChanged {
					//  ...either always, or only when the check is NOT flapping
					if !self.SuppressFlapping || !event.Check.IsFlapping() {
						if data, err := json.Marshal(event); err == nil {
							fmt.Printf("%s\n", data[:])
						}
					}
				}
			}
		}
	}
}

func (self *Reacter) Run() error {
	if len(self.Checks) > 0 {
		log.Infof("Start monitoring %d checks", len(self.Checks))

		for _, check := range self.Checks {
			log.Debugf("Starting monitor for check '%s' every %d seconds", check.Name, check.Interval)
			log.Debugf("%d observation(s) must fail to enter a failed state, %d observation(s) must pass to recover", check.Fall, check.Rise)
			go check.Monitor(self.Events)
		}

		self.StartEventProcessing()
	} else {
		return fmt.Errorf("No checks defined, nothing to do")
	}

	return nil
}

package reacter

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"time"

	"github.com/ghetzel/go-stockutil/executil"
	"github.com/ghetzel/go-stockutil/log"
	"github.com/ghetzel/go-stockutil/timeutil"
	"github.com/ghetzel/go-stockutil/typeutil"
	"github.com/ghetzel/reacter/util"
	"github.com/ghodss/yaml"
)

var DefaultConfigFile = executil.RootOrString(`/etc/reacter.yml`, `~/.config/reacter.yml`)
var DefaultConfigDir = executil.RootOrString(`/etc/reacter/conf.d`, `~/.config/reacter.d`)

type Reacter struct {
	NodeName         string
	Checks           []*Check
	Events           chan CheckEvent
	ConfigFile       string
	ConfigDir        string
	PrintJson        bool
	WriteJson        io.Writer
	OnlyPrintChanges bool
	SuppressFlapping bool
}

type Config struct {
	ChecksDefinitions []Check `json:"checks"`
}

func NewReacter() *Reacter {
	return &Reacter{
		ConfigFile: DefaultConfigFile,
		ConfigDir:  DefaultConfigDir,
		Checks:     make([]*Check, 0),
		Events:     make(chan CheckEvent),
	}
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

	if d := duration(checkConfig.Interval); d > 0 {
		check.Interval = d
	} else if d < 0 {
		return fmt.Errorf("Cannot specify a negative interval (%v)", d)
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

	if d := duration(checkConfig.Timeout); d > 0 {
		check.Timeout = d
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

// Loads the ConfigFile (if present), and recursively scans and load all *.yml files in ConfigDir.
func (self *Reacter) ReloadConfig() error {
	return util.LoadConfigFiles(self.ConfigFile, self.ConfigDir, self)
}

// Load the configuration file the given path and append any checks to this instance.
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
				var out string

				if event.Output != `` {
					out = `: ` + event.Output
				}

				switch event.Check.State {
				case SuccessState:
					log.Noticef("%s is healthy%s%s", event.Check.Name, suffix, out)
				case WarningState:
					log.Warningf("%s is in a warning state%s%s", event.Check.Name, suffix, out)
				default:
					log.Errorf("%s is in a critical state%s%s", event.Check.Name, suffix, out)
				}
			} else {
				log.Errorf("Check '%s' encountered an error during execution: %v", event.Check.Name, event.Output)

				//  put a few things into the check state because it failed too quickly to do that itself
				if event.Check.State != 128 {
					event.Check.StateChanged = true
				}

				event.Check.State = 128
			}

			//  serialize check and print as JSON
			if self.PrintJson || self.WriteJson != nil {
				//  ...either always, or only when the state has changed from its previous value
				if !self.OnlyPrintChanges || event.Check.StateChanged {
					//  ...either always, or only when the check is NOT flapping
					if !self.SuppressFlapping || !event.Check.IsFlapping() {
						if data, err := json.Marshal(event); err == nil {
							if self.PrintJson {
								fmt.Printf("%s\n", string(data))
							}

							if self.WriteJson != nil {
								fmt.Fprintf(self.WriteJson, "%s\n", string(data))
							}
						}
					}
				}
			}
		}
	}
}

func (self *Reacter) Run() error {
	if err := self.ReloadConfig(); err == nil {
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
	} else {
		return err
	}
}

func duration(in interface{}, fallback ...time.Duration) time.Duration {
	var fb time.Duration

	if len(fallback) > 0 {
		fb = fallback[0]
	}

	if dd, ok := in.(time.Duration); ok {
		if dd == 0 {
			return fb
		} else {
			return dd
		}
	} else if in == nil {
		return fb
	} else if d, err := timeutil.ParseDuration(typeutil.String(in)); err == nil {
		return d
	} else if v := typeutil.Int(in); v == 0 {
		return fb
	} else if time.Duration(v) < time.Microsecond {
		return time.Second * time.Duration(v)
	} else if time.Duration(v) < time.Millisecond {
		return time.Millisecond * time.Duration(v)
	} else {
		panic("invalid interval: " + err.Error())
	}
}

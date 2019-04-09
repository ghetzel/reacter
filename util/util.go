package util

import (
	"os"
	"path/filepath"
	"time"

	"github.com/ghetzel/go-stockutil/fileutil"
	"github.com/ghetzel/go-stockutil/log"
)

const ApplicationName = `reacter`
const ApplicationVersion = `1.0.4`
const ApplicationSummary = `a tool for generating, consuming, and handling system monitoring events`

var StartedAt = time.Now()

type Configurable interface {
	LoadConfig(path string) error
}

func LoadConfigFiles(configFile string, configDir string, configurable Configurable) error {
	if x, err := fileutil.ExpandUser(configFile); err == nil {
		configFile = x
	}

	if x, err := fileutil.ExpandUser(configDir); err == nil {
		configDir = x
	}

	if fileutil.IsNonemptyFile(configFile) {
		log.Infof("Loading: %s", configFile)

		if err := configurable.LoadConfig(configFile); err != nil {
			log.Errorf("Error loading %s: %v", configFile, err)
			return err
		}
	}

	if fileutil.IsNonemptyDir(configDir) {
		log.Debugf("Scanning for config files in %s", configDir)

		filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
			if filepath.Ext(path) == `.yml` && info.Mode().IsRegular() {
				log.Infof("Loading: %s", path)
				if err := configurable.LoadConfig(path); err != nil {
					log.Errorf("Error loading %s: %v", path, err)
					return err
				}
			}

			return nil
		})
	}

	return nil
}

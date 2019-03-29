package reacter

import (
	"fmt"
	"time"

	"github.com/ghetzel/go-stockutil/log"
)

const (
	DEFAULT_MAX_OBSERVATIONS = 21
	DEFAULT_FLAP_HIGH_THRESH = 0.5
	DEFAULT_FLAP_LOW_THRESH  = 0.25
	FLAP_BASE_COEFFICIENT    = 0.8
	FLAP_WEIGHT_MULTIPLIER   = 0.02
)

type Observation struct {
	Timestamp       time.Time              `json:"-"`
	State           ObservationState       `json:"-"`
	Output          []string               `json:"-"`
	Errors          []string               `json:"-"`
	PerformanceData map[string]Measurement `json:"measurements,omitempty"`
}

func (self *Observation) SetState(state int) {
	switch state {
	case 0:
		self.State = SuccessState
	case 1:
		self.State = WarningState
	default:
		self.State = CriticalState
	}
}

type Observations struct {
	Values            []Observation `json:"-"`
	Size              int           `json:"size"`
	Flapping          bool          `json:"flapping"`
	FlapDetect        bool          `json:"flap_detection"`
	FlapThresholdLow  float32       `json:"flap_threshold_low"`
	FlapThresholdHigh float32       `json:"flap_threshold_high"`
	StateChangeFactor float32       `json:"flap_factor"`
}

type ObservationState int32

const (
	SuccessState  ObservationState = 0
	WarningState                   = 1
	CriticalState                  = 2
	UnknownState                   = 3
)

func NewObservations() *Observations {
	rv := Observations{}
	rv.Values = make([]Observation, 0)
	rv.Size = DEFAULT_MAX_OBSERVATIONS
	rv.FlapDetect = true
	rv.FlapThresholdLow = DEFAULT_FLAP_LOW_THRESH
	rv.FlapThresholdHigh = DEFAULT_FLAP_HIGH_THRESH
	return &rv
}

func (self *Observations) Push(observation Observation) error {
	//  if this push would exceed the set size, shift off the first (oldest)
	//  element before pushing
	//
	if self.Size == 0 {
		return fmt.Errorf("Cannot push observation onto a zero-capacity observation set")
	}

	if len(self.Values) >= self.Size {
		self.Values = self.Values[1:]
	}

	self.Values = append(self.Values, observation)

	if self.FlapDetect {
		self.detectFlapping()
	}

	return nil
}

// Implements Nagios standard service flap detection as
// documented here: https://assets.nagios.com/downloads/nagioscore/docs/nagioscore/3/en/flapping.html
//
func (self *Observations) detectFlapping() bool {
	var stateChanges float32

	for i := 0; i < len(self.Values)-1; i++ {
		if self.Values[i+1].State != self.Values[i].State {
			//  this will calculate a weighted value based on how far back the
			//  observation occurred in the stack

			//
			stateChanges += (1.0 * (FLAP_BASE_COEFFICIENT + (float32(i) * FLAP_WEIGHT_MULTIPLIER)))
		}
	}

	self.StateChangeFactor = stateChanges / float32(len(self.Values))
	log.Debugf("  state change: %f/%d, percent: %f%%, flap threshold +%f / -%f", stateChanges, len(self.Values), self.StateChangeFactor, self.FlapThresholdHigh, self.FlapThresholdLow)

	if !self.Flapping {
		if self.StateChangeFactor > self.FlapThresholdHigh {
			self.Flapping = true
		}
	} else {
		if self.StateChangeFactor < self.FlapThresholdLow {
			self.Flapping = false
		}
	}

	return self.Flapping
}

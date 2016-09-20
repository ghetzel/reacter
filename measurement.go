package reacter

import (
	"strconv"
	"strings"
)

type MeasurementUnit int32

const (
	Unknown MeasurementUnit = 0
	Numeric                 = 1
	Time                    = 2
	Percent                 = 3
	Bytes                   = 4
	Counter                 = 5
)

type Measurement struct {
	Unit              MeasurementUnit `json:"unit"`
	Value             float32         `json:"value"`
	WarningThreshold  float32         `json:"warning"`
	CriticalThreshold float32         `json:"critical"`
	Minumum           float32         `json:"minimum"`
	Maximum           float32         `json:"maximum"`
}

func (self *Measurement) SetValues(valueUOM string, warn string, crit string, min string, max string) error {
	valueUOM = strings.ToLower(valueUOM)

	valueStr := strings.TrimFunc(valueUOM, func(c rune) bool {
		//  valid characters: - . 0 1 2 3 4 5 6 7 8 9
		if c == 46 || c == 45 || c >= 48 && c <= 57 {
			return false
		}

		return true
	})

	factor := float32(1.0)

	if strings.HasSuffix(valueUOM, `s`) {
		self.Unit = Time

		//  normalize all values as milliseconds
		if strings.HasSuffix(valueUOM, `ns`) {
			factor = 0.000001
		} else if strings.HasSuffix(valueUOM, `us`) {
			factor = 0.001
		} else if strings.HasSuffix(valueUOM, `ms`) {
			factor = 1.0
		} else if strings.HasSuffix(valueUOM, `s`) {
			factor = 1000.0
		}

	} else if strings.HasSuffix(valueUOM, `c`) {
		self.Unit = Counter
	} else if strings.HasSuffix(valueUOM, `%`) {
		self.Unit = Percent
	} else if strings.HasSuffix(valueUOM, `B`) {
		self.Unit = Bytes

		if strings.HasSuffix(valueUOM, `b`) {
			factor = 1.0
		} else if strings.HasSuffix(valueUOM, `kb`) {
			factor = 1024.0
		} else if strings.HasSuffix(valueUOM, `mb`) {
			factor = 1048576.0
		} else if strings.HasSuffix(valueUOM, `gb`) {
			factor = 1073741824.0
		} else if strings.HasSuffix(valueUOM, `tb`) {
			factor = 1099511627776.0
		} else if strings.HasSuffix(valueUOM, `pb`) {
			factor = 1125899906842624.0
		} else if strings.HasSuffix(valueUOM, `eb`) {
			factor = 1152921504606846976.0
		} else if strings.HasSuffix(valueUOM, `zb`) {
			factor = 1180591620717411303424.0
		} else if strings.HasSuffix(valueUOM, `yb`) {
			factor = 1208925819614629174706176.0
		}

	} else if valueUOM == `u` {
		self.Unit = Unknown
	} else {
		self.Unit = Numeric
	}

	//  parse value (sans UOM)
	if v, err := strconv.ParseFloat(valueStr, 32); err == nil {
		self.Value = float32(v) * factor
	} else {
		return err
	}

	//  parse warn
	if v, err := strconv.ParseFloat(warn, 32); err == nil {
		self.WarningThreshold = float32(v) * factor
	} else {
		return err
	}

	//  parse crit
	if v, err := strconv.ParseFloat(crit, 32); err == nil {
		self.CriticalThreshold = float32(v) * factor
	} else {
		return err
	}

	//  parse min
	if v, err := strconv.ParseFloat(min, 32); err == nil {
		self.Minumum = float32(v) * factor
	} else {
		return err
	}

	//  parse max
	if v, err := strconv.ParseFloat(max, 32); err == nil {
		self.Maximum = float32(v) * factor
	} else {
		return err
	}

	return nil
}

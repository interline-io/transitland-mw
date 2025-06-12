package limit

import (
	"context"
	"fmt"
	"time"

	"github.com/interline-io/log"
	"github.com/interline-io/transitland-mw/meters"
	"github.com/tidwall/gjson"
)

func init() {
	var _ meters.MeterProvider = &LimitMeterProvider{}
}

type LimitMeterProvider struct {
	Enabled       bool
	DefaultLimits []UserMeterLimit
	meters.MeterProvider
}

func NewLimitMeterProvider(provider meters.MeterProvider) *LimitMeterProvider {
	return &LimitMeterProvider{
		MeterProvider: provider,
	}
}

func (c *LimitMeterProvider) NewMeter(u meters.MeterUser) meters.Meterer {
	userName := ""
	userData := ""
	if u != nil {
		userName = u.ID()
		userData, _ = u.GetExternalData("gatekeeper")
	}
	return &LimitMeter{
		userId:   userName,
		userData: userData,
		provider: c,
		Meterer:  c.MeterProvider.NewMeter(u),
	}
}

type LimitMeter struct {
	userId   string
	userData string
	provider *LimitMeterProvider
	meters.Meterer
}

func (c *LimitMeter) GetLimits(meterName string, checkDims meters.Dimensions) []UserMeterLimit {
	// The limit matches the event dimensions if all of the LIMIT dimensions are contained in event
	var lims []UserMeterLimit
	for _, userLimit := range parseGkUserLimits(c.userData) {
		if userLimit.MeterName == meterName && meters.DimsContainedIn(userLimit.Dims, checkDims) {
			lims = append(lims, userLimit)
		}
	}
	for _, defaultLimit := range c.provider.DefaultLimits {
		if defaultLimit.MeterName == meterName && meters.DimsContainedIn(defaultLimit.Dims, checkDims) {
			lims = append(lims, defaultLimit)
		}
	}
	return lims
}

func (c *LimitMeter) Check(ctx context.Context, meterName string, value float64, extraDimensions meters.Dimensions) (bool, error) {
	if !c.provider.Enabled {
		return true, nil
	}
	for _, lim := range c.GetLimits(meterName, extraDimensions) {
		d1, d2 := lim.Span()
		currentValue, _ := c.GetValue(ctx, meterName, d1, d2, lim.Dims)
		if currentValue+value > lim.Limit {
			log.TraceCheck(func() {
				log.Trace().Str("meter", meterName).Str("user", c.userId).Float64("limit", lim.Limit).Float64("current", currentValue).Float64("add", value).Str("dims", fmt.Sprintf("%v", lim.Dims)).Msg("rate limited")
			})
			return false, nil
		} else {
			log.TraceCheck(func() {
				log.Trace().Str("meter", meterName).Str("user", c.userId).Float64("limit", lim.Limit).Float64("current", currentValue).Float64("add", value).Str("dims", fmt.Sprintf("%v", lim.Dims)).Msg("rate check: ok")
			})
		}
	}
	return true, nil
}

func (c *LimitMeter) Meter(ctx context.Context, meterEvent meters.MeterEvent) error {
	return c.Meterer.Meter(ctx, meterEvent)
}

func parseGkUserLimits(v string) []UserMeterLimit {
	var lims []UserMeterLimit
	for _, productLimit := range gjson.Get(v, "product_limits").Map() {
		for _, plim := range productLimit.Array() {
			lim := UserMeterLimit{
				MeterName: plim.Get("amberflo_meter").String(),
				Limit:     plim.Get("limit_value").Float(),
				Period:    plim.Get("time_period").String(),
			}
			if dim := plim.Get("amberflo_dimension").String(); dim != "" {
				lim.Dims = append(lim.Dims, meters.Dimension{
					Key:   dim,
					Value: plim.Get("amberflo_dimension_value").String(),
				})
			}
			lims = append(lims, lim)
		}
	}
	return lims
}

type UserMeterLimit struct {
	User      string
	MeterName string
	Dims      meters.Dimensions
	Period    string
	Limit     float64
}

func (lim *UserMeterLimit) Span() (time.Time, time.Time) {
	a, b, err := meters.PeriodSpan(lim.Period)
	if err != nil {
		panic(err)
	}
	return a, b
}

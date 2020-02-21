package main

import (
	"errors"
	"math"

	"github.com/DataDog/kafka-kit/kafkametrics"
)

// Limits is a map of instance-type to network bandwidth limits.
type Limits map[string]float64

// NewLimitsConfig is used to initialize
// a Limits.
type NewLimitsConfig struct {
	// Min throttle rate in MB/s.
	Minimum float64
	// Max throttle rate as a portion of capacity. // TODO deprecate.
	Maximum float64
	// Max source broker throttle rate as a portion of capacity.
	SourceMaximum float64
	// Max destination broker throttle rate as a portion of capacity.
	DestinationMaximum float64
	// Map of instance-type to total network capacity in MB/s.
	CapacityMap map[string]float64
}

// NewLimits takes a minimum float64 and a map of instance-type to
// float64 network capacity values (in MB/s).
func NewLimits(c NewLimitsConfig) (Limits, error) {
	switch {
	case c.Minimum <= 0:
		return nil, errors.New("minimum must be > 0")
	case c.Maximum <= 0 || c.Maximum > 100:
		return nil, errors.New("maximum must be > 0 and < 100")
	case c.SourceMaximum <= 0 || c.SourceMaximum > 100:
		return nil, errors.New("source maximum must be > 0 and < 100")
	case c.DestinationMaximum <= 0 || c.DestinationMaximum > 100:
		return nil, errors.New("destination maximum must be > 0 and < 100")
	}

	// Populate the min/max vals into the Limits map.
	lim := Limits{
		"minimum": c.Minimum,
		"maximum": c.Maximum,
		"srcMax":  c.SourceMaximum,
		"dstMax":  c.DestinationMaximum,
	}

	// Update with provided
	// capacity map.
	for k, v := range c.CapacityMap {
		lim[k] = v
	}

	return lim, nil
}

// headroom takes a *kafkametrics.Broker and last set throttle rate and
// returns the headroom based on utilization vs capacity. Headroom is
// determined by subtracting the current throttle rate from the current
// outbound network utilization. This yields a crude approximation of
// how much non-replication throughput is currently being demanded.
// The non-replication throughput is then subtracted from the total
// network capacity available. This value suggests what headroom is
// available for replication. We then use the greater of:
// - this value * the configured portion of free bandwidth eligible for replication
// - the configured minimum replication rate in MB/s
func (l Limits) headroom(b *kafkametrics.Broker, t float64) (float64, error) {
	if b == nil {
		return l["minimum"], errors.New("Nil broker provided")
	}

	if capacity, exists := l[b.InstanceType]; exists {
		nonThrottleUtil := math.Max(b.NetTX-t, 0.00)
		// Determine if/how far over the target capacity
		// we are. This is also subtracted from the available
		// headroom.
		overCap := math.Max(b.NetTX-capacity, 0.00)

		return math.Max((capacity-nonThrottleUtil-overCap)*(l["maximum"]/100), l["minimum"]), nil
	}

	return l["minimum"], errors.New("Unknown instance type")
}

// replicationHeadroom takes a *kafkametrics.Broker, what type of replica role
// it's fulfilling, and the last set throttle rate. A replication headroom value
// is returned based on utilization vs capacity. Headroom is determined by
// subtracting the current throttle rate from the current network utilization.
// This yields a crude approximation of how much non-replication throughput is
// currently being demanded. The non-replication throughput is then subtracted
// from the total network capacity available. This value suggests what headroom
// is available for replication. We then use the greater of:
// - this value * the configured portion of free bandwidth eligible for replication
// - the configured minimum replication rate in MB/s
func (l Limits) replicationHeadroom(b *kafkametrics.Broker, rt replicaType, prevThrottle float64) (float64, error) {
	var currNetUtilization float64
	var maxRatio float64

	switch rt {
	case "leader":
		currNetUtilization = b.NetTX
		maxRatio = l["srcMax"]
	case "follower":
		currNetUtilization = b.NetRX
		maxRatio = l["dstMax"]
	default:
		return 0.00, errors.New("invalid replica type")
	}

	if capacity, exists := l[b.InstanceType]; exists {
		nonThrottleUtil := math.Max(b.NetTX-prevThrottle, 0.00)
		// Determine if/how far over the target capacity
		// we are. This is also subtracted from the available
		// headroom.
		overCap := math.Max(currNetUtilization-capacity, 0.00)

		return math.Max((capacity-nonThrottleUtil-overCap)*(maxRatio/100), l["minimum"]), nil
	}

	return l["minimum"], errors.New("Unknown instance type")
}

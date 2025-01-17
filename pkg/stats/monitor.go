package stats

import (
	"fmt"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/atomic"

	"github.com/livekit/ingress/pkg/config"
	"github.com/livekit/ingress/pkg/errors"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/utils"
)

type Monitor struct {
	cpuCostConfig config.CPUCostConfig
	maxCost       float64

	promCPULoad  prometheus.Gauge
	requestGauge *prometheus.GaugeVec

	cpuStats *utils.CPUStats

	pendingCPUs atomic.Float64
}

func NewMonitor() *Monitor {
	return &Monitor{}
}

func (m *Monitor) Start(conf *config.Config) error {
	cpuStats, err := utils.NewCPUStats(func(idle float64) {
		m.promCPULoad.Set(1 - idle/float64(m.cpuStats.NumCPU()))
	})
	if err != nil {
		return err
	}
	m.cpuStats = cpuStats

	if err := m.checkCPUConfig(conf.CPUCost); err != nil {
		return err
	}

	m.promCPULoad = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace:   "livekit",
		Subsystem:   "node",
		Name:        "cpu_load",
		ConstLabels: prometheus.Labels{"node_id": conf.NodeID, "node_type": "INGRESS"},
	})
	promNodeAvailable := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace:   "livekit",
		Subsystem:   "ingress",
		Name:        "available",
		ConstLabels: prometheus.Labels{"node_id": conf.NodeID},
	}, func() float64 {
		c := m.CanAcceptIngress()
		if c {
			return 1
		}
		return 0
	})
	m.requestGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace:   "livekit",
		Subsystem:   "ingress",
		Name:        "requests",
		ConstLabels: prometheus.Labels{"node_id": conf.NodeID},
	}, []string{"type", "transcoding"})

	prometheus.MustRegister(m.promCPULoad, promNodeAvailable, m.requestGauge)

	return nil
}

func (m *Monitor) Stop() {
	if m.cpuStats != nil {
		m.cpuStats.Stop()
	}
}

func (m *Monitor) checkCPUConfig(costConfig config.CPUCostConfig) error {
	if costConfig.RTMPCpuCost < 1 {
		logger.Warnw("rtmp input requirement too low", nil,
			"config value", costConfig.RTMPCpuCost,
			"minimum value", 1,
			"recommended value", 2,
		)
	}

	if costConfig.WHIPCpuCost < 1 {
		logger.Warnw("whip input requirement too low", nil,
			"config value", costConfig.WHIPCpuCost,
			"minimum value", 1,
			"recommended value", 2,
		)
	}

	if costConfig.WHIPBypassTranscodingCpuCost < 0.05 {
		logger.Warnw("whip input with transcoding bypassed requirement too low", nil,
			"config value", costConfig.WHIPCpuCost,
			"minimum value", 0.05,
			"recommended value", 0.1,
		)
	}

	requirements := []float64{
		costConfig.RTMPCpuCost,
		costConfig.WHIPCpuCost,
		costConfig.WHIPBypassTranscodingCpuCost,
	}
	sort.Float64s(requirements)
	m.maxCost = requirements[len(requirements)-1]

	recommendedMinimum := m.maxCost
	if recommendedMinimum < 3 {
		recommendedMinimum = 3
	}

	if float64(m.cpuStats.NumCPU()) < requirements[0] {
		logger.Errorw("not enough cpu", nil,
			"minimum cpu", requirements[0],
			"recommended", recommendedMinimum,
			"available", float64(m.cpuStats.NumCPU()),
		)
		return errors.ErrServerCapacityExceeded
	}

	if float64(m.cpuStats.NumCPU()) < m.maxCost {
		logger.Errorw("not enough cpu for some ingress types", nil,
			"minimum cpu", m.maxCost,
			"recommended", recommendedMinimum,
			"available", m.cpuStats.NumCPU(),
		)
	}

	logger.Infow(fmt.Sprintf("available CPU cores: %d max cost: %f", m.cpuStats.NumCPU(), m.maxCost))

	return nil
}

func (m *Monitor) GetCPULoad() float64 {
	return (float64(m.cpuStats.NumCPU()) - m.cpuStats.GetCPUIdle()) / float64(m.cpuStats.NumCPU()) * 100
}

func (m *Monitor) CanAcceptIngress() bool {
	available := m.cpuStats.GetCPUIdle() - m.pendingCPUs.Load()

	return available > m.maxCost
}

func (m *Monitor) AcceptIngress(info *livekit.IngressInfo) bool {
	var cpuHold float64
	var accept bool
	available := m.cpuStats.GetCPUIdle() - m.pendingCPUs.Load()

	switch info.InputType {
	case livekit.IngressInput_RTMP_INPUT:
		accept = available > m.cpuCostConfig.RTMPCpuCost
		cpuHold = m.cpuCostConfig.RTMPCpuCost
	case livekit.IngressInput_WHIP_INPUT:
		if info.BypassTranscoding {
			accept = available > m.cpuCostConfig.WHIPBypassTranscodingCpuCost
			cpuHold = m.cpuCostConfig.WHIPBypassTranscodingCpuCost
		} else {
			accept = available > m.cpuCostConfig.WHIPCpuCost
			cpuHold = m.cpuCostConfig.WHIPCpuCost
		}

	default:
		logger.Errorw("unsupported request type", errors.New("invalid parameter"))
	}

	if accept {
		m.pendingCPUs.Add(cpuHold)
		time.AfterFunc(time.Second, func() { m.pendingCPUs.Sub(cpuHold) })
	}

	logger.Debugw("cpu request", "accepted", accept, "availableCPUs", available, "numCPUs", m.cpuStats.NumCPU())
	return accept
}

func (m *Monitor) IngressStarted(info *livekit.IngressInfo) {
	switch info.InputType {
	case livekit.IngressInput_RTMP_INPUT:
		m.requestGauge.With(prometheus.Labels{"type": "rtmp", "transcoding": fmt.Sprintf("%v", !info.BypassTranscoding)}).Add(1)
	case livekit.IngressInput_WHIP_INPUT:
		m.requestGauge.With(prometheus.Labels{"type": "whip", "transcoding": fmt.Sprintf("%v", !info.BypassTranscoding)}).Add(1)
	}

}

func (m *Monitor) IngressEnded(info *livekit.IngressInfo) {
	switch info.InputType {
	case livekit.IngressInput_RTMP_INPUT:
		m.requestGauge.With(prometheus.Labels{"type": "rtmp", "transcoding": fmt.Sprintf("%v", !info.BypassTranscoding)}).Sub(1)
	case livekit.IngressInput_WHIP_INPUT:
		m.requestGauge.With(prometheus.Labels{"type": "whip", "transcoding": fmt.Sprintf("%v", !info.BypassTranscoding)}).Sub(1)

	}
}

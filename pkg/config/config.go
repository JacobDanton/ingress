package config

import (
	"os"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/livekit/ingress/pkg/errors"
	"github.com/livekit/mediatransportutil/pkg/rtcconfig"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/redis"
	"github.com/livekit/protocol/utils"
	"github.com/livekit/psrpc"
	lksdk "github.com/livekit/server-sdk-go"
)

const (
	DefaultRTMPPort      int = 1935
	DefaultWHIPPort          = 8080
	DefaultHTTPRelayPort     = 9090
)

var (
	DefaultICEPortRange = []uint16{2000, 4000}
)

type Config struct {
	Redis     *redis.RedisConfig `yaml:"redis"`      // required
	ApiKey    string             `yaml:"api_key"`    // required (env LIVEKIT_API_KEY)
	ApiSecret string             `yaml:"api_secret"` // required (env LIVEKIT_API_SECRET)
	WsUrl     string             `yaml:"ws_url"`     // required (env LIVEKIT_WS_URL)

	HealthPort     int           `yaml:"health_port"`
	PrometheusPort int           `yaml:"prometheus_port"`
	RTMPPort       int           `yaml:"rtmp_port"` // -1 to disable RTMP
	WHIPPort       int           `yaml:"whip_port"` // -1 to disable WHIP
	HTTPRelayPort  int           `yaml:"http_relay_port"`
	Logging        logger.Config `yaml:"logging"`
	Development    bool          `yaml:"development"`

	// Used for WHIP transport
	RTCConfig rtcconfig.RTCConfig `yaml:"rtc_config"`

	// CPU costs for various ingress types
	CPUCost CPUCostConfig `yaml:"cpu_cost"`

	// internal
	ServiceName string `yaml:"-"`
	NodeID      string `yaml:"-"`
}

type WhipConfig struct {
	// TODO add IceLite, NAT1To1IPs
	ICEPortRange            []uint16 `yaml:"ice_port_range"`
	EnableLoopbackCandidate bool     `yaml:"enable_loopback_candidate"`
}

type CPUCostConfig struct {
	RTMPCpuCost                  float64 `yaml:"rtmp_cpu_cost"`
	WHIPCpuCost                  float64 `yaml:"whip_cpu_cost"`
	WHIPBypassTranscodingCpuCost float64 `yaml:"whip_bypass_transcoding_cpu_cost"`
}

func NewConfig(confString string) (*Config, error) {
	conf := &Config{
		ApiKey:      os.Getenv("LIVEKIT_API_KEY"),
		ApiSecret:   os.Getenv("LIVEKIT_API_SECRET"),
		WsUrl:       os.Getenv("LIVEKIT_WS_URL"),
		ServiceName: "ingress",
		NodeID:      utils.NewGuid("NE_"),
	}
	if confString != "" {
		if err := yaml.Unmarshal([]byte(confString), conf); err != nil {
			return nil, errors.ErrCouldNotParseConfig(err)
		}
	}

	err := conf.Init()
	if err != nil {
		return nil, err
	}

	if conf.Redis == nil {
		return nil, psrpc.NewErrorf(psrpc.InvalidArgument, "redis configuration is required")
	}

	return conf, nil
}

func (conf *Config) Init() error {
	if conf.RTMPPort == 0 {
		conf.RTMPPort = DefaultRTMPPort
	}
	if conf.HTTPRelayPort == 0 {
		conf.HTTPRelayPort = DefaultHTTPRelayPort
	}
	if conf.WHIPPort == 0 {
		conf.WHIPPort = DefaultWHIPPort
	}

	err := conf.InitWhipConf()
	if err != nil {
		return err
	}

	if err := conf.InitLogger(); err != nil {
		return err
	}

	return nil
}

func (c *Config) InitWhipConf() error {
	if c.WHIPPort <= 0 {
		return nil
	}

	err := c.RTCConfig.Validate(c.Development)
	if err != nil {
		return err
	}

	return nil
}

func (c *Config) InitLogger(values ...interface{}) error {
	zl, err := logger.NewZapLogger(&c.Logging)
	if err != nil {
		return err
	}

	values = append(c.GetLoggerValues(), values...)
	l := zl.WithValues(values...)
	logger.SetLogger(l, c.ServiceName)
	lksdk.SetLogger(l)

	return nil
}

// To use with zap logger
func (c *Config) GetLoggerValues() []interface{} {
	return []interface{}{"nodeID", c.NodeID}
}

// To use with logrus
func (c *Config) GetLoggerFields() logrus.Fields {
	fields := logrus.Fields{
		"logger": c.ServiceName,
	}
	v := c.GetLoggerValues()
	for i := 0; i < len(v); i += 2 {
		fields[v[i].(string)] = v[i+1]
	}

	return fields
}

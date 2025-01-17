//go:build integration

package test

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/livekit/ingress/pkg/rtmp"
	"github.com/livekit/ingress/pkg/service"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/rpc"
	"github.com/livekit/psrpc"
	"github.com/stretchr/testify/require"
)

func RunRTMPTest(t *testing.T, conf *TestConfig, bus psrpc.MessageBus, svc *service.Service, commandPsrpcClient rpc.IngressHandlerClient) {
	rtmpsrv := rtmp.NewRTMPServer()
	relay := service.NewRelay(rtmpsrv, nil)

	err := rtmpsrv.Start(conf.Config, svc.HandleRTMPPublishRequest)
	require.NoError(t, err)
	err = relay.Start(conf.Config)
	require.NoError(t, err)

	t.Cleanup(func() {
		relay.Stop()
		rtmpsrv.Stop()
	})

	updates := make(chan *rpc.UpdateIngressStateRequest, 10)
	ios := &ioServer{}
	ios.updateIngressState = func(req *rpc.UpdateIngressStateRequest) error {
		updates <- req
		return nil
	}

	info := &livekit.IngressInfo{
		IngressId:           "ingress_id",
		InputType:           livekit.IngressInput_RTMP_INPUT,
		Name:                "ingress-test",
		RoomName:            conf.RoomName,
		ParticipantIdentity: "ingress-test",
		ParticipantName:     "ingress-test",
		Reusable:            true,
		StreamKey:           "ingress-test",
		Url:                 "rtmp://localhost:1935/live/ingress-test",
		Audio: &livekit.IngressAudioOptions{
			Name:   "audio",
			Source: 0,
			EncodingOptions: &livekit.IngressAudioOptions_Options{
				Options: &livekit.IngressAudioEncodingOptions{
					AudioCodec: livekit.AudioCodec_OPUS,
					Bitrate:    64000,
					DisableDtx: false,
					Channels:   2,
				},
			},
		},
		Video: &livekit.IngressVideoOptions{
			Name:   "video",
			Source: 0,
			EncodingOptions: &livekit.IngressVideoOptions_Options{
				Options: &livekit.IngressVideoEncodingOptions{
					VideoCodec: livekit.VideoCodec_H264_BASELINE,
					FrameRate:  20,
					Layers: []*livekit.VideoLayer{
						{
							Quality: livekit.VideoQuality_HIGH,
							Width:   1280,
							Height:  720,
							Bitrate: 3000000,
						},
					},
				},
			},
		},
	}
	ios.getIngressInfo = func(req *rpc.GetIngressInfoRequest) (*rpc.GetIngressInfoResponse, error) {
		return &rpc.GetIngressInfoResponse{Info: info, WsUrl: conf.WsUrl}, nil
	}

	ioPsrpc, err := rpc.NewIOInfoServer("ingress_test_server", ios, bus)
	require.NoError(t, err)
	t.Cleanup(func() {
		ioPsrpc.Kill()
	})

	time.Sleep(time.Second)

	logger.Infow("rtmp url", "url", info.Url)

	cmdString := strings.Split(
		fmt.Sprintf(
			"gst-launch-1.0 -v flvmux name=mux ! rtmp2sink location=%s  "+
				"audiotestsrc freq=200 ! faac ! mux.  "+
				"videotestsrc pattern=ball is-live=true ! video/x-raw,width=1280,height=720 ! x264enc speed-preset=3 tune=zerolatency ! mux.",
			info.Url),
		" ")
	cmd := exec.Command(cmdString[0], cmdString[1:]...)
	require.NoError(t, cmd.Start())

	time.Sleep(time.Second * 45)

	_, err = commandPsrpcClient.DeleteIngress(context.Background(), info.IngressId, &livekit.DeleteIngressRequest{IngressId: info.IngressId})
	require.NoError(t, err)

	time.Sleep(time.Second * 2)

	final := <-updates
	require.NotEqual(t, final.State.Status, livekit.IngressState_ENDPOINT_ERROR)
}

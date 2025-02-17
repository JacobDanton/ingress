package service

import (
	"context"

	google_protobuf2 "google.golang.org/protobuf/types/known/emptypb"

	"github.com/frostbyte73/core"
	"github.com/livekit/ingress/pkg/config"
	"github.com/livekit/ingress/pkg/errors"
	"github.com/livekit/ingress/pkg/media"
	"github.com/livekit/ingress/pkg/params"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/rpc"
	"github.com/livekit/protocol/tracer"
)

type Handler struct {
	conf      *config.Config
	pipeline  *media.Pipeline
	rpcClient rpc.IOInfoClient
	kill      core.Fuse
	done      core.Fuse
}

func NewHandler(conf *config.Config, rpcClient rpc.IOInfoClient) *Handler {
	return &Handler{
		conf:      conf,
		rpcClient: rpcClient,
		kill:      core.NewFuse(),
		done:      core.NewFuse(),
	}
}

func (h *Handler) HandleIngress(ctx context.Context, info *livekit.IngressInfo, wsUrl, token string, extraParams any) {
	ctx, span := tracer.Start(ctx, "Handler.HandleRequest")
	defer span.End()

	p, err := h.buildPipeline(ctx, info, wsUrl, token, extraParams)
	if err != nil {
		span.RecordError(err)
		return
	}
	h.pipeline = p

	// start ingress
	result := make(chan *livekit.IngressInfo, 1)
	go func() {
		result <- p.Run(ctx)
		h.done.Break()
	}()

	kill := h.kill.Watch()

	for {
		select {
		case <-kill:
			// kill signal received
			p.SendEOS(ctx)
			kill = nil

		case res := <-result:
			// ingress finished
			h.sendUpdate(ctx, res)
			return
		}
	}
}

func (h *Handler) killAndReturnState(ctx context.Context) (*livekit.IngressState, error) {
	h.Kill()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.done.Watch():
		return h.pipeline.State, nil
	}
}

func (h *Handler) UpdateIngress(ctx context.Context, req *livekit.UpdateIngressRequest) (*livekit.IngressState, error) {
	_, span := tracer.Start(ctx, "Handler.UpdateIngress")
	defer span.End()
	return h.killAndReturnState(ctx)
}

func (h *Handler) DeleteIngress(ctx context.Context, req *livekit.DeleteIngressRequest) (*livekit.IngressState, error) {
	_, span := tracer.Start(ctx, "Handler.DeleteIngress")
	defer span.End()
	return h.killAndReturnState(ctx)
}

func (h *Handler) DeleteWHIPResource(ctx context.Context, req *rpc.DeleteWHIPResourceRequest) (*google_protobuf2.Empty, error) {
	_, span := tracer.Start(ctx, "Handler.DeleteWHIPResource")
	defer span.End()

	h.killAndReturnState(ctx)

	return &google_protobuf2.Empty{}, nil
}

func (h *Handler) buildPipeline(ctx context.Context, info *livekit.IngressInfo, wsUrl, token string, extraParams any) (*media.Pipeline, error) {
	ctx, span := tracer.Start(ctx, "Handler.buildPipeline")
	defer span.End()

	// build/verify params
	var p *media.Pipeline
	params, err := params.GetParams(ctx, h.conf, info, wsUrl, token, extraParams)
	if err == nil {
		// create the pipeline
		p, err = media.New(ctx, h.conf, params)
	}

	if err != nil {
		if params != nil {
			info = params.IngressInfo
		}

		info.State.Error = err.Error()
		info.State.Status = livekit.IngressState_ENDPOINT_ERROR
		h.sendUpdate(ctx, info)
		return nil, err
	}

	p.OnStatusUpdate(h.sendUpdate)
	return p, nil
}

func (h *Handler) sendUpdate(ctx context.Context, info *livekit.IngressInfo) {
	switch info.State.Status {
	case livekit.IngressState_ENDPOINT_ERROR:
		logger.Warnw("ingress failed", errors.New(info.State.Error),
			"ingressID", info.IngressId,
		)
	case livekit.IngressState_ENDPOINT_INACTIVE:
		logger.Infow("ingress complete", "ingressID", info.IngressId)
	default:
		logger.Infow("ingress update", "ingressID", info.IngressId)
	}

	_, err := h.rpcClient.UpdateIngressState(ctx, &rpc.UpdateIngressStateRequest{
		IngressId: info.IngressId,
		State:     info.State,
	})
	if err != nil {
		logger.Errorw("failed to send update", err)
	}
}

func (h *Handler) Kill() {
	h.kill.Break()
}

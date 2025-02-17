package media

import (
	"github.com/tinyzimmer/go-gst/gst"

	"github.com/livekit/ingress/pkg/errors"
	"github.com/livekit/protocol/livekit"
)

type VideoOutputBin struct {
	bin                  *gst.Bin
	preProcessorElements []*gst.Element
	tee                  *gst.Element
}

func NewVideoOutputBin(options *livekit.IngressVideoEncodingOptions, outputs []*Output) (*VideoOutputBin, error) {
	o := &VideoOutputBin{}

	o.bin = gst.NewBin("video output bin")

	if options.FrameRate > 0 {
		videoRate, err := gst.NewElement("videorate")
		if err != nil {
			return nil, err
		}
		if err = videoRate.SetProperty("max-rate", int(options.FrameRate)); err != nil {
			return nil, err
		}
		o.preProcessorElements = append(o.preProcessorElements, videoRate)
	}

	videoConvert, err := gst.NewElement("videoconvert")
	if err != nil {
		return nil, err
	}
	o.preProcessorElements = append(o.preProcessorElements, videoConvert)

	err = o.bin.AddMany(o.preProcessorElements...)
	if err != nil {
		return nil, err
	}

	err = gst.ElementLinkMany(o.preProcessorElements...)
	if err != nil {
		return nil, err
	}

	o.tee, err = gst.NewElement("tee")
	if err != nil {
		return nil, err
	}

	err = o.bin.Add(o.tee)
	if err != nil {
		return nil, err
	}

	err = o.preProcessorElements[len(o.preProcessorElements)-1].Link(o.tee)

	for _, output := range outputs {
		err := o.bin.Add(output.bin.Element)
		if err != nil {
			return nil, err
		}

		err = gst.ElementLinkMany(o.tee, output.bin.Element)
		if err != nil {
			return nil, err
		}
	}

	binSink := gst.NewGhostPad("sink", o.preProcessorElements[0].GetStaticPad("sink"))
	if !o.bin.AddPad(binSink.Pad) {
		return nil, errors.ErrUnableToAddPad
	}

	return o, nil
}

func (o *VideoOutputBin) GetBin() *gst.Bin {
	return o.bin
}

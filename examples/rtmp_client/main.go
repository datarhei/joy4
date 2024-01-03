package main

import (
	"fmt"
	"log"
	"os"

	"github.com/datarhei/joy4/av"
	"github.com/datarhei/joy4/av/avutil"
	"github.com/datarhei/joy4/codec/h264parser"
	"github.com/datarhei/joy4/format"
)

func init() {
	format.RegisterAll()
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("%s [url]", os.Args[0])
	}

	src, err := avutil.Open(os.Args[1])
	if err != nil {
		log.Fatalf("error connecting: %s", err.Error())
	}

	defer src.Close()

	var streams []av.CodecData

	if streams, err = src.Streams(); err != nil {
		log.Fatalf("error streams: %s", err.Error())
	}

	idx := int8(-1)
	for i, s := range streams {
		if s.Type().IsVideo() {
			fmt.Printf("video: %s\n", s.Type().String())
			v := s.(h264parser.CodecData)
			os.Stdout.Write(v.AVCDecoderConfRecordBytes())
			idx = int8(i)
		}
	}

	for {
		p, err := src.ReadPacket()
		if err != nil {
			log.Fatalf("error reading: %s", err.Error())
		}

		if p.Idx != idx {
			continue
		}

		os.Stdout.Write(p.Data)
	}
}

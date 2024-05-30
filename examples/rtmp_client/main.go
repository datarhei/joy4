package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/datarhei/joy4/av"
	"github.com/datarhei/joy4/codec/h264parser"
	"github.com/datarhei/joy4/format"
	"github.com/datarhei/joy4/format/rtmp"
)

func init() {
	format.RegisterAll()
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("%s [url]", os.Args[0])
	}

	src, err := rtmp.Dial(os.Args[1], rtmp.DialOptions{
		MaxProbePacketCount: 10,
		DebugChunks: func(conn net.Conn) bool {
			return true
		},
	})
	if err != nil {
		log.Fatalf("error connecting: %s", err.Error())
	}

	src.SetIdleTimeout(10 * time.Second)

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
			fmt.Printf("%s", hex.Dump(v.AVCDecoderConfRecordBytes()))
			idx = int8(i)
		}
	}

	var bytes uint64 = 0

	src.SetWriteIdleTimeout(0)

	for {
		p, err := src.ReadPacket()
		if err != nil {
			log.Fatalf("error reading: %s", err.Error())
		}

		if p.Idx != idx {
			continue
		}

		bytes += uint64(len(p.Data))

		fmt.Printf("%d\r", bytes)
	}
}

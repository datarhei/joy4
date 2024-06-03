package main

import (
	"fmt"
	"io"

	"github.com/datarhei/joy4/av"
	"github.com/datarhei/joy4/format/rtmp"
)

func main() {
	server := &rtmp.Server{
		Addr: ":3035",
	}

	server.HandlePublish = func(src *rtmp.Conn) {
		defer src.Close()

		url := fmt.Sprintf("rtmp://127.0.0.1:2035%s", src.URL.Path)
		dst, err := rtmp.Dial(url, rtmp.DialOptions{})
		if err != nil {
			return
		}

		defer dst.Close()

		streams, err := src.Streams()
		if err != nil {
			return
		}

		err = dst.WriteHeader(streams)
		if err != nil {
			return
		}

		npackets := uint64(0)
		for {
			var pkt av.Packet

			if pkt, err = src.ReadPacket(); err != nil {
				if err == io.EOF {
					break
				}
				return
			}

			npackets++

			if npackets < 100 {
				if err = dst.WritePacket(pkt); err != nil {
					return
				}
			}
		}

		err = dst.WriteTrailer()
		if err != nil {
			return
		}
	}

	server.ListenAndServe()
}

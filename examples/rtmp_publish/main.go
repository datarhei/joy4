package main

import (
	"github.com/datarhei/joy4/av/avutil"
	"github.com/datarhei/joy4/av/pktque"
	"github.com/datarhei/joy4/format"
	"github.com/datarhei/joy4/format/rtmp"
)

func init() {
	format.RegisterAll()
}

// as same as: ffmpeg -re -i projectindex.flv -c copy -f flv rtmp://localhost:1936/app/publish

func main() {
	file, _ := avutil.Open("projectindex.flv")
	conn, _ := rtmp.Dial("rtmp://localhost:1936/app/publish")
	// conn, _ := avutil.Create("rtmp://localhost:1936/app/publish")

	demuxer := &pktque.FilterDemuxer{Demuxer: file, Filter: &pktque.Walltime{}}
	avutil.CopyFile(conn, demuxer)

	file.Close()
	conn.Close()
}

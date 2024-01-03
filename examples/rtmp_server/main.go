package main

import (
	"flag"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/datarhei/joy4/av/avutil"
	"github.com/datarhei/joy4/av/pktque"
	"github.com/datarhei/joy4/av/pubsub"
	"github.com/datarhei/joy4/format"
	"github.com/datarhei/joy4/format/flv/flvio"
	"github.com/datarhei/joy4/format/rtmp"
)

func init() {
	format.RegisterAll()
}

type channel struct {
	que      *pubsub.Queue
	metadata flvio.AMFMap
	hasAudio bool
	hasVideo bool
}

type Config struct {
	Addr  string
	App   string
	Token string
}

type Server interface {
	ListenAndServe() error
	ListenAndServeTLS(certFile, keyFile string) error
	Channels() []string
}

type server struct {
	app   string
	token string

	server *rtmp.Server

	channels map[string]*channel
	lock     sync.RWMutex
}

var _ Server = &server{}

func New(config Config) (Server, error) {
	if len(config.App) == 0 {
		config.App = "/"
	}

	s := &server{
		app:   config.App,
		token: config.Token,
	}

	s.server = &rtmp.Server{
		Addr: config.Addr,
	}

	s.channels = make(map[string]*channel)

	s.server.HandlePlay = s.handlePlay
	s.server.HandlePublish = s.handlePublish

	rtmp.Debug = false

	return s, nil
}

func (s *server) ListenAndServe() error {
	return s.server.ListenAndServe()
}

func (s *server) ListenAndServeTLS(certFile, keyFile string) error {
	return s.server.ListenAndServeTLS(certFile, keyFile)
}

func (s *server) Channels() []string {
	channels := []string{}

	s.lock.RLock()
	defer s.lock.RUnlock()

	for key := range s.channels {
		channels = append(channels, key)
	}

	return channels
}

func (s *server) log(who, action, path, message string, client net.Addr) {
	fmt.Printf("%-7s %10s %s (%s) %s\n", who, action, path, client, message)
}

func (s *server) handlePlay(conn *rtmp.Conn) {
	client := conn.NetConn().RemoteAddr()

	q := conn.URL.Query()
	token := q.Get("token")

	if len(s.token) != 0 && s.token != token {
		s.log("PLAY", "FORBIDDEN", conn.URL.Path, "invalid token", client)
		conn.Close()
		return
	}

	s.lock.RLock()
	ch := s.channels[conn.URL.Path]
	s.lock.RUnlock()

	if ch != nil {
		//conn.SetMetaData(ch.metadata)

		s.log("PLAY", "START", conn.URL.Path, "", client)
		cursor := ch.que.Oldest()

		filters := pktque.Filters{}

		if ch.hasVideo {
			filters = append(filters, &pktque.WaitKeyFrame{})
		}

		filters = append(filters, &pktque.FixTime{StartFromZero: true, MakeIncrement: false})

		demuxer := &pktque.FilterDemuxer{
			Filter:  filters,
			Demuxer: cursor,
		}

		avutil.CopyFile(conn, demuxer)
		s.log("PLAY", "STOP", conn.URL.Path, "", client)
	} else {
		s.log("PLAY", "NOTFOUND", conn.URL.Path, "", client)
	}
}

func (s *server) handlePublish(conn *rtmp.Conn) {
	client := conn.NetConn().RemoteAddr()

	q := conn.URL.Query()
	token := q.Get("token")

	if len(s.token) != 0 && s.token != token {
		s.log("PUBLISH", "FORBIDDEN", conn.URL.Path, "invalid token", client)
		conn.Close()
		return
	}

	if !strings.HasPrefix(conn.URL.Path, s.app) {
		s.log("PUBLISH", "FORBIDDEN", conn.URL.Path, "invalid app", client)
		conn.Close()
		return
	}

	streams, _ := conn.Streams()

	if len(streams) == 0 {
		s.log("PUBLISH", "INVALID", conn.URL.Path, "no streams available", client)
		conn.Close()
		return
	}

	//metadata := conn.GetMetaData()

	s.lock.Lock()

	ch := s.channels[conn.URL.Path]
	if ch == nil {
		ch = &channel{}
		//ch.metadata = metadata
		ch.que = pubsub.NewQueue()
		ch.que.WriteHeader(streams)
		for _, stream := range streams {
			typ := stream.Type()

			switch {
			case typ.IsAudio():
				ch.hasAudio = true
			case typ.IsVideo():
				ch.hasVideo = true
			}
		}

		s.channels[conn.URL.Path] = ch
	} else {
		ch = nil
	}

	s.lock.Unlock()

	if ch == nil {
		s.log("PUBLISH", "CONFLICT", conn.URL.Path, "already publishing", client)
		conn.Close()
		return
	}

	s.log("PUBLISH", "START", conn.URL.Path, "", client)

	for _, stream := range streams {
		s.log("PUBLISH", "STREAM", conn.URL.Path, stream.Type().String(), client)
	}

	err := avutil.CopyPackets(ch.que, conn)
	if err != nil {
		s.log("PUBLISH", "ERROR", conn.URL.Path, err.Error(), client)
	}

	s.lock.Lock()
	delete(s.channels, conn.URL.Path)
	s.lock.Unlock()

	ch.que.Close()

	s.log("PUBLISH", "STOP", conn.URL.Path, "", client)
}

func main() {
	var cert string
	var key string
	var help bool

	flag.StringVar(&cert, "cert", "", "Path to the certifacate file")
	flag.StringVar(&key, "key", "", "Path to the key file")
	flag.BoolVar(&help, "h", false, "Show options")

	flag.Parse()

	config := Config{
		Addr: ":1935",
	}

	server, _ := New(config)

	var err error

	if len(cert) == 0 && len(key) == 0 {
		fmt.Printf("Started RTMP server. Listening on %s\n", config.Addr)
		err = server.ListenAndServe()
	} else {
		fmt.Printf("Started RTMPS server. Listening on %s\n", config.Addr)
		err = server.ListenAndServeTLS(cert, key)
	}

	fmt.Printf("%s\n", err)
}

package rtmp

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/nareix/joy5/av"
	"github.com/nareix/joy5/format/flv"
	log "github.com/sirupsen/logrus"

	"github.com/gabek/owncast/config"
	"github.com/gabek/owncast/core"
	"github.com/gabek/owncast/core/ffmpeg"
	"github.com/gabek/owncast/utils"
	"github.com/nareix/joy5/codec/h264"
	"github.com/nareix/joy5/format/rtmp"
)

var (
	//IsConnected whether there is a connection or not
	_isConnected = false
)

var _transcoder ffmpeg.Transcoder
var _pipe *os.File

//Start starts the rtmp service, listening on port 1935
func Start() {
	port := 1935
	s := rtmp.NewServer()
	var lis net.Listener
	var error error
	if lis, error = net.Listen("tcp", fmt.Sprintf(":%d", port)); error != nil {
		return
	}

	s.LogEvent = func(c *rtmp.Conn, nc net.Conn, e int) {
		es := rtmp.EventString[e]
		log.Traceln(unsafe.Pointer(c), nc.LocalAddr(), nc.RemoteAddr(), es)
	}

	s.HandleConn = HandleConn

	if error != nil {
		log.Panicln(error)
	}
	log.Infof("RTMP server is listening for incoming stream on port: %d", port)

	for {
		nc, err := lis.Accept()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		go s.HandleNetConn(nc)
	}
}

func HandleConn(c *rtmp.Conn, nc net.Conn) {
	if _isConnected {
		log.Errorln("stream already running; can not overtake an existing stream")
		nc.Close()
		return
	}

	streamingKeyComponents := strings.Split(c.URL.Path, "/")
	streamingKey := streamingKeyComponents[len(streamingKeyComponents)-1]
	if streamingKey != config.Config.VideoSettings.StreamingKey {
		log.Errorln("invalid streaming key; rejecting incoming stream")
		nc.Close()
		return
	}

	log.Infoln("Incoming RTMP connected.")

	pipePath := utils.GetTemporaryPipePath()
	syscall.Mkfifo(pipePath, 0666)

	_transcoder = ffmpeg.NewTranscoder()
	go _transcoder.Start()

	_isConnected = true
	core.SetStreamAsConnected()

	f, err := os.OpenFile(pipePath, os.O_RDWR, os.ModeNamedPipe)
	_pipe = f
	fmt.Println(pipePath)
	if err != nil {
		panic(err)
	}

	w := flv.NewMuxer(f)

	for {
		pkt, err := c.ReadPacket()
		if err != nil {
			if err == io.EOF {
				handleDisconnect(nc)
			}
		}

		if pkt.Type == av.H264 {
			nalus, _ := h264.SplitNALUs(pkt.Data)
			annexb := h264.JoinNALUsAnnexb(nalus)
			avcc := h264.JoinNALUsAVCC([][]byte{annexb})
			pkt.Data = avcc
		}

		if err := w.WritePacket(pkt); err != nil {
			panic(err)
		}
	}

}

func handleDisconnect(conn net.Conn) {
	log.Infoln("RTMP disconnected.")
	conn.Close()
	_pipe.Close()
	_isConnected = false
	_transcoder.Stop()
	core.SetStreamAsDisconnected()
}

//IsConnected gets whether there is an rtmp connection or not
//this is only a getter since it is controlled by the rtmp handler
func IsConnected() bool {
	return _isConnected
}

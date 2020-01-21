//go:generate sh -c "protoc -I . -I \"$(go list -f '{{ .Dir }}' -m github.com/gogo/protobuf)/protobuf\" --gogofaster_out=. pingpong.proto"

package pingpong

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/janos/bee/pkg/p2p"
	"github.com/janos/bee/pkg/p2p/protobuf"
)

const (
	protocolName  = "pingpong"
	streamName    = "pingpong"
	streamVersion = "1.0.0"
)

type Service struct {
	p2p p2p.Service
}

func New(p2ps p2p.Service) (s *Service, err error) {
	s = &Service{
		p2p: p2ps,
	}

	if err := p2ps.AddProtocol(p2p.ProtocolSpec{
		Name: protocolName,
		StreamSpecs: []p2p.StreamSpec{
			{
				Name:    streamName,
				Version: streamVersion,
				Handler: s.Handler,
			},
		},
	}); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) Handler(p p2p.Peer) {
	w, r := protobuf.NewRW(p.Stream)
	defer p.Stream.Close()

	var ping Ping
	for {
		if err := r.ReadMsg(&ping); err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("pingpong handler: read message: %v\n", err)
			return
		}
		log.Printf("got ping: %q\n", ping.Greeting)

		if err := w.WriteMsg(&Pong{
			Response: ping.Greeting,
		}); err != nil {
			log.Printf("pingpong handler: write message: %v\n", err)
			return
		}
	}
}

func (s *Service) Ping(ctx context.Context, peerID string, msgs ...string) (rtt time.Duration, err error) {
	stream, err := s.p2p.NewStream(ctx, peerID, protocolName, streamName, streamVersion)
	if err != nil {
		return 0, fmt.Errorf("new stream: %w", err)
	}
	defer stream.Close()

	w, r := protobuf.NewRW(stream)

	var wg sync.WaitGroup
	//var mtx sync.Mutex
	start := time.Now()
	go func() {
		for {
			fmt.Println("reader loop")
			var pong Pong
			if err := r.ReadMsg(&pong); err != nil {
				if err == io.EOF {
					fmt.Println("got eof, returning")
					return
				}
				//return 0, err
			}
			wg.Done()
			log.Printf("got pong: %q\n", pong.Response)
		}
	}()

	for _, msg := range msgs {
		wg.Add(2)
		go func(msg string) {
			defer wg.Done()
			fmt.Println("sending msg")
			if err := w.WriteMsg(&Ping{
				Greeting: msg,
			}); err != nil {
				fmt.Println(fmt.Errorf("stream write: %w", err))
			}
		}(msg)
	}

	wg.Wait()
	return time.Since(start) / time.Duration(len(msgs)), nil
}

package libp2p

import (
	"context"
	"fmt"
	"time"

	"github.com/multiformats/go-multistream"

	"github.com/janos/bee/pkg/p2p"

	"github.com/libp2p/go-libp2p"
	autonat "github.com/libp2p/go-libp2p-autonat-svc"
	connmgr "github.com/libp2p/go-libp2p-connmgr"
	"github.com/libp2p/go-libp2p-core/helpers"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/peerstore"
	protocol "github.com/libp2p/go-libp2p-core/protocol"
	"github.com/libp2p/go-libp2p-core/routing"
	libp2pquic "github.com/libp2p/go-libp2p-quic-transport"
	secio "github.com/libp2p/go-libp2p-secio"
	libp2ptls "github.com/libp2p/go-libp2p-tls"
	ma "github.com/multiformats/go-multiaddr"
)

var _ p2p.Service = new(Service)

type Service struct {
	host host.Host
}

type Options struct {
	Port       int
	ListenIPv4 string
	PrivKey    []byte
	Routing    func(host.Host) (routing.PeerRouting, error)
}

func New(ctx context.Context, o Options) (*Service, error) {
	ipV4Addr := o.ListenIPv4
	if ipV4Addr == "" {
		ipV4Addr = "0.0.0.0"
	}
	opts := []libp2p.Option{
		// Use the keypair we generated
		//libp2p.Identity(priv),
		// Multiple listen addresses
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/%s/tcp/%v", ipV4Addr, o.Port),      // regular tcp connections
			fmt.Sprintf("/ip4/%s/udp/%v/quic", ipV4Addr, o.Port), // a UDP endpoint for the QUIC transport
		),
		// support TLS connections
		libp2p.Security(libp2ptls.ID, libp2ptls.New),
		// support secio connections
		libp2p.Security(secio.ID, secio.New),
		// support QUIC - experimental
		libp2p.Transport(libp2pquic.NewTransport),
		// support any other default transports (TCP)
		libp2p.DefaultTransports,
		// Let's prevent our peer from having too many
		// connections by attaching a connection manager.
		libp2p.ConnectionManager(connmgr.NewConnManager(
			100,         // Lowwater
			400,         // HighWater,
			time.Minute, // GracePeriod
		)),
		// Attempt to open ports using uPNP for NATed hosts.
		libp2p.NATPortMap(),
		// Let this host use the DHT to find other hosts
		//libp2p.Routing(o.Routing),
		// Let this host use relays and advertise itself on relays if
		// it finds it is behind NAT. Use libp2p.Relay(options...) to
		// enable active relays and more.
		//libp2p.EnableAutoRelay(),
	}

	h, err := libp2p.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	// If you want to help other peers to figure out if they are behind
	// NATs, you can launch the server-side of AutoNAT too (AutoRelay
	// already runs the client)
	if _, err = autonat.NewAutoNATService(ctx, h,
		// Support same non default security and transport options as
		// original host.
		libp2p.Security(libp2ptls.ID, libp2ptls.New),
		libp2p.Security(secio.ID, secio.New),
		libp2p.Transport(libp2pquic.NewTransport),
		libp2p.DefaultTransports,
	); err != nil {
		return nil, fmt.Errorf("autonat: %w", err)
	}

	return &Service{host: h}, nil
}

func (s *Service) AddProtocol(p p2p.ProtocolSpec) (err error) {
	for _, ss := range p.StreamSpecs {
		id := protocol.ID(p2p.NewSwarmStreamName(p.Name, ss.Name, ss.Version))
		_, err := helpers.MultistreamSemverMatcher(id)
		if err != nil {
			return fmt.Errorf("match semver %s: %w", id, err)
		}
		s.host.SetStreamHandler(id, func(s network.Stream) {
			ss.Handler(p2p.Peer{
				Addr:   s.Conn().RemoteMultiaddr(),
				Stream: stream{s},
			})
		})
	}
	return nil
}

func (s *Service) Addresses() (addrs []string, err error) {
	// Build host multiaddress
	hostAddr, err := ma.NewMultiaddr(fmt.Sprintf("/p2p/%s", s.host.ID().Pretty()))
	if err != nil {
		return nil, err
	}

	// Now we can build a full multiaddress to reach this host
	// by encapsulating both addresses:
	for _, addr := range s.host.Addrs() {
		addrs = append(addrs, addr.Encapsulate(hostAddr).String())
	}
	return addrs, nil
}

func (s *Service) Connect(ctx context.Context, addr ma.Multiaddr) (peerID string, err error) {
	// Extract the peer ID from the multiaddr.
	info, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		return "", err
	}

	s.host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)

	return info.ID.String(), nil
}

func (s *Service) NewStream(ctx context.Context, peerID, protocolName, streamName, version string) (p2p.Stream, error) {
	id, err := peer.Decode(peerID)
	if err != nil {
		return nil, fmt.Errorf("decode peer id %q: %w", peerID, err)
	}
	swarmStreamName := p2p.NewSwarmStreamName(protocolName, streamName, version)
	st, err := s.host.NewStream(ctx, id, protocol.ID(swarmStreamName))
	if err != nil {
		if err == multistream.ErrNotSupported || err == multistream.ErrIncorrectVersion {
			return nil, p2p.NewIncompatibleStreamError(err)
		}
		return nil, fmt.Errorf("create stream %q to %q: %w", swarmStreamName, peerID, err)
	}
	return stream{st}, nil
}

type stream struct {
	network.Stream
}

func (s stream) Close() error {
	return helpers.FullClose(s)
}

package main

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
	record "github.com/libp2p/go-libp2p-record"
	"github.com/libp2p/go-msgio/protoio"
	"github.com/multiformats/go-multiaddr"

	dhtpb "github.com/libp2p/go-libp2p-kad-dht/pb"
	recpb "github.com/libp2p/go-libp2p-record/pb"
)

func dhtProtocolMessenger(ctx context.Context, proto protocol.ID, ai *peer.AddrInfo) (*dhtpb.ProtocolMessenger, error) {
	h, err := libp2pHost(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.Connect(ctx, *ai); err != nil {
		return nil, err
	}

	ms := &dhtMsgSender{
		h:         h,
		protocols: []protocol.ID{proto},
		timeout:   time.Second * 5,
	}
	messenger, err := dhtpb.NewProtocolMessenger(ms, dhtpb.WithValidator(&nilValidator{}))
	if err != nil {
		return nil, err
	}

	return messenger, nil
}

func dhtPut(ctx context.Context, key, value []byte, proto protocol.ID, ma multiaddr.Multiaddr) error {
	ai, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return err
	}

	m, err := dhtProtocolMessenger(ctx, proto, ai)
	if err != nil {
		return err
	}

	return m.PutValue(ctx, ai.ID, &recpb.Record{Key: key, Value: value})
}

func dhtGet(ctx context.Context, key []byte, proto protocol.ID, ma multiaddr.Multiaddr) (*recpb.Record, error) {
	ai, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return nil, err
	}

	m, err := dhtProtocolMessenger(ctx, proto, ai)
	if err != nil {
		return nil, err
	}

	rec, _, err := m.GetValue(ctx, ai.ID, string(key))
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// dhtMsgSender handles sending dht wire protocol messages to a given peer
type dhtMsgSender struct {
	h         host.Host
	protocols []protocol.ID
	timeout   time.Duration
}

// SendRequest sends a peer a message and waits for its response
func (ms *dhtMsgSender) SendRequest(ctx context.Context, p peer.ID, pmes *dhtpb.Message) (*dhtpb.Message, error) {
	s, err := ms.h.NewStream(ctx, p, ms.protocols...)
	if err != nil {
		return nil, err
	}

	w := protoio.NewDelimitedWriter(s)
	if err := w.WriteMsg(pmes); err != nil {
		return nil, err
	}

	r := protoio.NewDelimitedReader(s, network.MessageSizeMax)
	tctx, cancel := context.WithTimeout(ctx, ms.timeout)
	defer cancel()
	defer func() { _ = s.Close() }()

	msg := new(dhtpb.Message)
	if err := ctxReadMsg(tctx, r, msg); err != nil {
		_ = s.Reset()
		return nil, err
	}

	return msg, nil
}

func ctxReadMsg(ctx context.Context, rc protoio.ReadCloser, mes *dhtpb.Message) error {
	errc := make(chan error, 1)
	go func(r protoio.ReadCloser) {
		defer close(errc)
		err := r.ReadMsg(mes)
		errc <- err
	}(rc)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SendMessage sends a peer a message without waiting on a response
func (ms *dhtMsgSender) SendMessage(ctx context.Context, p peer.ID, pmes *dhtpb.Message) error {
	s, err := ms.h.NewStream(ctx, p, ms.protocols...)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	w := protoio.NewDelimitedWriter(s)
	return w.WriteMsg(pmes)
}

var _ dhtpb.MessageSender = (*dhtMsgSender)(nil)

type nilValidator struct{}

func (n nilValidator) Validate(key string, value []byte) error {
	return nil
}

func (n nilValidator) Select(key string, values [][]byte) (int, error) {
	panic("implement me")
}

var _ record.Validator = (*nilValidator)(nil)
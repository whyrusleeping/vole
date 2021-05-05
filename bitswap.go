package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multiaddr"

	nrouting "github.com/ipfs/go-ipfs-routing/none"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-core/peer"
	quic "github.com/libp2p/go-libp2p-quic-transport"
	"github.com/libp2p/go-tcp-transport"

	bsmsg "github.com/ipfs/go-bitswap/message"
	bsmsgpb "github.com/ipfs/go-bitswap/message/pb"
	bsnet "github.com/ipfs/go-bitswap/network"
)

type bsCheckOutput struct {
	Found     bool
	Responded bool
	Error     error
}

func checkBitswapCmd(ctx context.Context, cidStr, maStr string) error {
	bsCid, err := cid.Decode(cidStr)
	if err != nil {
		return err
	}

	ma, err := multiaddr.NewMultiaddr(maStr)
	if err != nil {
		return err
	}

	output, err := checkBitswapCID(ctx, bsCid, ma)
	if err != nil {
		return err
	}

	jsOut, err := json.Marshal(output)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", jsOut)
}

func checkBitswapCID(ctx context.Context, c cid.Cid, ma multiaddr.Multiaddr) (*bsCheckOutput, error) {
	h, err := libp2p.New(ctx, libp2p.Transport(tcp.NewTCPTransport), libp2p.Transport(quic.NewTransport))
	if err != nil {
		return nil, err
	}

	ai, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return nil, err
	}

	if err := h.Connect(ctx, *ai); err != nil {
		return nil, err
	}

	target := ai.ID

	nilRouter, err := nrouting.ConstructNilRouting(nil, nil, nil, nil)
	if err != nil {
		return nil, err
	}

	bs := bsnet.NewFromIpfsHost(h, nilRouter)
	msg := bsmsg.New(false)
	msg.AddEntry(c, 0, bsmsgpb.Message_Wantlist_Have, true)

	rcv := &bsReceiver{
		target: target,
		result: make(chan msgOrErr),
	}

	bs.SetDelegate(rcv)

	if err := bs.SendMessage(ctx, target, msg); err != nil {
		return nil, err
	}

	// in case for some reason we're sent a bunch of messages (e.g. wants) from a peer without them responding to our query
	// FIXME: Why would this be the case?
	sctx, _ := context.WithTimeout(ctx, time.Second*10)
loop:
	for {
		var res msgOrErr
		select {
		case res = <-rcv.result:
		case <-sctx.Done():
			break loop
		}

		if res.err != nil {
			return &bsCheckOutput{
				Found:     false,
				Responded: true,
				Error:     res.err,
			}, nil
		}

		if res.msg == nil {
			panic("should not be reachable")
		}

		for _, msgC := range res.msg.Blocks() {
			if msgC.Cid().Equals(c) {
				return &bsCheckOutput{
					Found:     true,
					Responded: true,
					Error:     nil,
				}, nil
			}
		}

		for _, msgC := range res.msg.Haves() {
			if msgC.Equals(c) {
				return &bsCheckOutput{
					Found:     true,
					Responded: true,
					Error:     nil,
				}, nil
			}
		}

		for _, msgC := range res.msg.DontHaves() {
			if msgC.Equals(c) {
				return &bsCheckOutput{
					Found:     false,
					Responded: true,
					Error:     nil,
				}, nil
			}
		}
	}

	return &bsCheckOutput{
		Found:     false,
		Responded: false,
		Error:     nil,
	}, nil
}

type bsReceiver struct {
	target peer.ID
	result chan msgOrErr
}

type msgOrErr struct {
	msg bsmsg.BitSwapMessage
	err error
}

func (r *bsReceiver) ReceiveMessage(ctx context.Context, sender peer.ID, incoming bsmsg.BitSwapMessage) {
	if r.target != sender {
		select {
		case <-ctx.Done():
		case r.result <- msgOrErr{err: fmt.Errorf("expected peerID %v, got %v", r.target, sender)}:
		}
		return
	}

	select {
	case <-ctx.Done():
	case r.result <- msgOrErr{msg: incoming}:
	}
}

func (r *bsReceiver) ReceiveError(err error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
	case r.result <- msgOrErr{err: err}:
	}
}

func (r *bsReceiver) PeerConnected(id peer.ID) {
	return
}

func (r *bsReceiver) PeerDisconnected(id peer.ID) {
	return
}

var _ bsnet.Receiver = (*bsReceiver)(nil)
/*
 * Copyright (C) 2021 The poly network Authors
 * This file is part of The poly network library.
 *
 * The  poly network  is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The  poly network  is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 * You should have received a copy of the GNU Lesser General Public License
 * along with The poly network .  If not, see <http://www.gnu.org/licenses/>.
 */

package neo

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/joeqian10/neo-gogogo/helper"
	"github.com/joeqian10/neo-gogogo/sc"

	nw "github.com/joeqian10/neo-gogogo/wallet"
	"github.com/polynetwork/bridge-common/base"
	"github.com/polynetwork/bridge-common/chains/neo"
	"github.com/polynetwork/bridge-common/chains/poly"
	"github.com/polynetwork/bridge-common/log"
	"github.com/polynetwork/bridge-common/util"
	"github.com/polynetwork/bridge-common/wallet"
	"github.com/polynetwork/poly-relayer/bus"
	"github.com/polynetwork/poly-relayer/config"
	"github.com/polynetwork/poly-relayer/msg"
)

const (
	VERIFY_AND_EXECUTE_TX = "VerifyAndExecuteTx"
	GET_CURRENT_HEIGHT    = "currentSyncHeight"
	CHANGE_BOOK_KEEPER    = "ChangeBookKeeper"
	SYNC_BLOCK_HEADER     = "SyncBlockHeader"
)

type Submitter struct {
	context.Context
	wg     *sync.WaitGroup
	config *config.SubmitterConfig
	sdk    *neo.SDK
	name   string
	ccd    string
	ccm    string
	polyId uint64
	wallet *wallet.NeoWallet
}

func (s *Submitter) Init(config *config.SubmitterConfig) (err error) {
	s.config = config
	s.sdk, err = neo.WithOptions(config.ChainId, config.Nodes, time.Minute, 1)
	if err != nil {
		return
	}
	if config.Wallet != nil {
		sdk, err := neo.WithOptions(config.ChainId, config.Wallet.Nodes, time.Minute, 1)
		if err != nil {
			return err
		}
		w := wallet.NewNeoWallet(config.Wallet, sdk)
		err = w.Init()
		if err != nil {
			return err
		}
		s.wallet = w
	}
	s.name = base.GetChainName(config.ChainId)
	s.polyId = poly.ReadChainID()
	if s.polyId == 0 {
		err = fmt.Errorf("Poly chain id not set for neo submitter")
	}
	return
}

func (s *Submitter) Submit(msg msg.Message) error {
	return nil
}

func (s *Submitter) submit(tx *msg.Tx) error {
	return nil
}

func (s *Submitter) Hook(ctx context.Context, wg *sync.WaitGroup, ch <-chan msg.Message) error {
	s.Context = ctx
	s.wg = wg
	return nil
}

func (s *Submitter) ProcessTx(m *msg.Tx, compose msg.PolyComposer) (err error) {
	if m.Type() != msg.POLY {
		return fmt.Errorf("%s desired message is not poly tx %v", m.Type())
	}

	if m.DstChainId != s.config.ChainId {
		return fmt.Errorf("%s message dst chain does not match %v", m.DstChainId)
	}
	h, err := s.sdk.Node().GetPolyEpochHeight(s.ccm, s.polyId)
	if err != nil {
		return fmt.Errorf("%s fetch dst chain poly epoch height error %v", s.name, err)
	}
	m.DstPolyEpochStartHeight = uint32(h)
	err = compose(m)
	if err != nil {
		return
	}
	return s.processPolyTx(m)
}

func ContractByteParam(v []byte) sc.ContractParameter {
	return sc.ContractParameter{Type: sc.ByteArray, Value: v}
}

func (s *Submitter) processPolyTx(tx *msg.Tx) (err error) {
	proof, err := hex.DecodeString(tx.AnchorProof)
	if err != nil {
		return fmt.Errorf("%s processPolyTx decode anchor proof hex error %v", s.name, err)
	}
	path, err := hex.DecodeString(tx.AuditPath)
	if err != nil {
		return
	}
	scriptHash := helper.HexToBytes(s.ccm)
	args := []sc.ContractParameter{
		ContractByteParam(path),
		ContractByteParam(tx.PolyHeader.GetMessage()),
		ContractByteParam(proof),
		ContractByteParam(tx.AnchorHeader.GetMessage()),
		ContractByteParam(tx.PolySigs),
	}
	builder := sc.NewScriptBuilder()
	builder.MakeInvocationScript(scriptHash, VERIFY_AND_EXECUTE_TX, args)
	script := builder.ToArray()
	tx.DstHash, err = s.wallet.Invoke(script, nil)
	return
}

func (s *Submitter) processPolyHeader(tx *msg.Tx) (err error) {
	cp1 := sc.ContractParameter{
		Type:  sc.ByteArray,
		Value: []byte{},
	}

	// public keys
	bs1 := []byte{}
	cp2 := sc.ContractParameter{
		Type:  sc.ByteArray,
		Value: bs1,
	}

	// signatures
	bs2 := []byte{}
	/*
		for _, sig := range block.Header.SigData {
			newSig, _ := signature.ConvertToEthCompatible(sig) // convert to eth
			bs2 = append(bs2, newSig...)
		}
	*/
	cp3 := sc.ContractParameter{
		Type:  sc.ByteArray,
		Value: bs2,
	}

	scriptHash := helper.HexToBytes(s.ccm)
	builder := sc.NewScriptBuilder()
	builder.MakeInvocationScript(scriptHash, SYNC_BLOCK_HEADER, []sc.ContractParameter{cp1, cp2, cp3})
	script := builder.ToArray()
	tx.DstHash, err = s.wallet.Invoke(script, nil)
	return
}

func (s *Submitter) run(account *nw.Account, bus bus.TxBus, compose msg.PolyComposer) error {
	s.wg.Add(1)
	defer s.wg.Done()
	for {
		select {
		case <-s.Done():
			log.Info("Submitter is exiting now", "chain", s.name)
			return nil
		default:
		}
		tx, err := bus.Pop(s.Context)
		if err != nil {
			log.Error("Bus pop error", "err", err)
			continue
		}
		if tx == nil {
			time.Sleep(time.Second)
			continue
		}
		log.Info("Processing poly tx", "poly_hash", tx.PolyHash, "account", account.Address)
		err = s.ProcessTx(tx, compose)
		if err != nil {
			log.Error("Process poly tx error", "chain", s.name, "err", err)
			fmt.Println(util.Verbose(tx))
			if errors.Is(err, msg.ERR_INVALID_TX) {
				log.Error("Skipped invalid poly tx", "poly_hash", tx.PolyHash)
				continue
			}
			tx.Attempts++
			bus.Push(context.Background(), tx)
		} else {
			log.Info("Submitted poly tx", "poly_hash", tx.PolyHash, "chain", s.name, "dst_hash", tx.DstHash)
		}
	}
}

func (s *Submitter) Start(ctx context.Context, wg *sync.WaitGroup, bus bus.TxBus, composer msg.PolyComposer) error {
	s.Context = ctx
	s.wg = wg
	accounts := s.wallet.Accounts
	if len(accounts) == 0 {
		log.Warn("No account available for submitter workers", "chain", s.name)
	}
	for i, a := range accounts {
		log.Info("Starting submitter worker", "index", i, "total", len(accounts), "account", a.Address, "chain", s.name)
		go s.run(a, bus, composer)
	}
	return nil
}

func (s *Submitter) Stop() error {
	s.wg.Wait()
	return nil
}

func (s *Submitter) Process(m msg.Message, compose msg.PolyComposer) (err error) {
	tx, ok := m.(*msg.Tx)
	if !ok {
		return fmt.Errorf("%s Proccess: Invalid poly tx cast %v", s.name, m)
	}
	return s.ProcessTx(tx, compose)
}

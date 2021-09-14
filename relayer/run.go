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

package relayer

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/polynetwork/bridge-common/log"
	"github.com/polynetwork/bridge-common/util"
	"github.com/polynetwork/poly-relayer/config"
)

type Server struct {
	ctx    context.Context
	wg     *sync.WaitGroup
	config *config.Config
	roles  []Handler
}

func Start(ctx context.Context, wg *sync.WaitGroup, config *config.Config) error {
	server := &Server{ctx, wg, config, nil}
	return server.Start()
}

func (s *Server) Start() (err error) {
	// Create poly tx sync handler
	if s.config.Poly != nil {
		s.parseHandlers(s.config.Poly.PolyTxSync)
	}

	// Create handlers
	for _, chain := range s.config.Chains {
		s.parseHandlers(chain.HeaderSync, chain.SrcTxSync, chain.SrcTxCommit, chain.PolyTxCommit)
	}

	// Initialize
	for i, handler := range s.roles {
		log.Info("Initializing role", "index", i, "total", len(s.roles), "type", reflect.TypeOf(handler), "chain", handler.Chain())
		err = handler.Init(s.ctx, s.wg)
		if err != nil {
			return
		}
	}

	// Start the roles
	for i, handler := range s.roles {
		log.Info("Starting role", "index", i, "total", len(s.roles), "type", reflect.TypeOf(handler), "chain", handler.Chain())
		err = handler.Start()
		if err != nil {
			return
		}
	}
	return
}

func (s *Server) parseHandlers(confs ...interface{}) {
	for _, conf := range confs {
		handler := s.parseHandler(conf)
		if handler != nil {
			s.roles = append(s.roles, handler)
		}
	}
}

func (s *Server) parseHandler(conf interface{}) (handler Handler) {
	if reflect.ValueOf(conf).IsZero() || !reflect.ValueOf(conf).Elem().FieldByName("Enabled").Interface().(bool) {
		return
	}
	switch c := conf.(type) {
	case *config.HeaderSyncConfig:
		handler = NewHeaderSyncHandler(c)
	case *config.SrcTxSyncConfig:
		handler = NewSrcTxSyncHandler(c)
	case *config.SrcTxCommitConfig:
		handler = NewSrcTxCommitHandler(c)
	case *config.PolyTxSyncConfig:
		handler = NewPolyTxSyncHandler(c)
	case *config.PolyTxCommitConfig:
		handler = NewPolyTxCommitHandler(c)
	default:
		log.Error("Unknown config type", "conf", conf)
	}
	if handler != nil {
		log.Info("Creating handler", "type", reflect.TypeOf(handler))
		fmt.Println(util.Verbose(conf))
	}
	return
}

func retry(f func() error, interval time.Duration) {
	var err error
	for {
		err = f()
		if err == nil {
			return
		}
		time.Sleep(interval)
	}
}

// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package rpc

import (
	"github.com/kaspanet/kaspad/logger"
	"github.com/kaspanet/kaspad/util/panics"
)

var (
	log, _ = logger.Get(logger.SubsystemTags.RPCS)
	spawn  = panics.GoroutineWrapperFunc(log)
)

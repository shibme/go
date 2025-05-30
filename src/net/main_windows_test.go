// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

import (
	"internal/poll"
	"os/exec"
	"syscall"
)

var (
	// Placeholders for saving original socket system calls.
	origWSASocket   = wsaSocketFunc
	origClosesocket = poll.CloseFunc
	origConnect     = connectFunc
	origConnectEx   = poll.ConnectExFunc
	origListen      = listenFunc
	origAccept      = poll.AcceptFunc
)

func installTestHooks() {
	wsaSocketFunc = sw.WSASocket
	poll.CloseFunc = sw.Closesocket
	connectFunc = sw.Connect
	poll.ConnectExFunc = sw.ConnectEx
	listenFunc = sw.Listen
	poll.AcceptFunc = sw.AcceptEx
}

func uninstallTestHooks() {
	wsaSocketFunc = origWSASocket
	poll.CloseFunc = origClosesocket
	connectFunc = origConnect
	poll.ConnectExFunc = origConnectEx
	listenFunc = origListen
	poll.AcceptFunc = origAccept
}

// forceCloseSockets must be called only from TestMain.
func forceCloseSockets() {
	for s := range sw.Sockets() {
		poll.CloseFunc(s)
	}
}

func addCmdInheritedHandle(cmd *exec.Cmd, fd uintptr) {
	// Inherited handles are not inherited by default in Windows.
	// We need to set the handle inheritance flag explicitly.
	// See https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessa#parameters
	// for more details.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, syscall.Handle(fd))
}

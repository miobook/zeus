package zeusmaster

import (
	"math/rand"
	"os"
	"strconv"
	"syscall"

	slog "github.com/burke/zeus/go/shinylog"
	"github.com/burke/zeus/go/unixsocket"
)

type SlaveMonitor struct {
	tree             *ProcessTree
	remoteMasterFile *os.File
}

func StartSlaveMonitor(tree *ProcessTree, quit chan bool) {
	localMasterFile, remoteMasterFile, err := unixsocket.Socketpair(syscall.SOCK_DGRAM)
	if err != nil {
		Error("Couldn't create socketpair")
	}

	monitor := &SlaveMonitor{tree, remoteMasterFile}

	localMasterSocket, err := unixsocket.NewUsockFromFile(localMasterFile)
	if err != nil {
		Error("Couldn't Open UNIXSocket")
	}

	// We just want this unix socket to be a channel so we can select on it...
	registeringFds := make(chan int, 3)
	go func() {
		for {
			fd, err := localMasterSocket.ReadFD()
			if err != nil {
				slog.Error(err)
			}
			registeringFds <- fd
		}
	}()

	for _, slave := range monitor.tree.SlavesByName {
		go slave.Run(monitor)
	}

	for {
		select {
		case <-quit:
			monitor.cleanupChildren()
			quit <- true
			return
		case fd := <-registeringFds:
			go monitor.slaveDidBeginRegistration(fd)
		}
	}
}

func (mon *SlaveMonitor) cleanupChildren() {
	for _, slave := range mon.tree.SlavesByName {
		slave.ForceKill()
	}
}

func (mon *SlaveMonitor) slaveDidBeginRegistration(fd int) {
	// Having just started the process, we expect an IO, which we convert to a UNIX domain socket
	fileName := strconv.Itoa(rand.Int())
	slaveFile := unixsocket.FdToFile(fd, fileName)
	slaveUsock, err := unixsocket.NewUsockFromFile(slaveFile)
	if err != nil {
		slog.Error(err)
	}
	if err = slaveUsock.Conn.SetReadBuffer(1024); err != nil {
		slog.Error(err)
	}
	if err = slaveUsock.Conn.SetWriteBuffer(1024); err != nil {
		slog.Error(err)
	}

	// We now expect the slave to use this fd they send us to send a Pid&Identifier Message
	msg, err := slaveUsock.ReadMessage()
	if err != nil {
		slog.Error(err)
	}
	pid, identifier, err := ParsePidMessage(msg)

	slaveNode := mon.tree.FindSlaveByName(identifier)
	if slaveNode == nil {
		Error("slavemonitor.go:slaveDidBeginRegistration:Unknown identifier:" + identifier)
	}

	slaveNode.SlaveWasInitialized(pid, slaveUsock)
}

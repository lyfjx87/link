package main

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/funny/link"
	_ "github.com/funny/unitest"
	"io"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	serverAddr  = flag.String("addr", "127.0.0.1:10010", "echo server address")
	clientNum   = flag.Int("num", 1, "client number")
	messageSize = flag.Int("size", 64, "test message size")
	runTime     = flag.Int("time", 10, "benchmark run time in seconds")
	proces      = flag.Int("procs", 1, "how many benchmark process")
	waitMaster  = flag.Bool("wait", false, "DO NOT USE")
)

type CountConn struct {
	net.Conn
	SendCount  uint32
	RecvCount  uint32
	ReadCount  uint32
	WriteCount uint32
}

func (conn *CountConn) Read(p []byte) (n int, err error) {
	n, err = conn.Conn.Read(p)
	atomic.AddUint32(&conn.ReadCount, 1)
	return
}

func (conn *CountConn) Write(p []byte) (n int, err error) {
	n, err = conn.Conn.Write(p)
	atomic.AddUint32(&conn.WriteCount, 1)
	return
}

type Message []byte

func (msg Message) Send(conn *link.Conn) error {
	conn.WritePacket(msg, link.SplitByUint16BE)
	return nil
}

func (msg Message) Receive(conn *link.Conn) error {
	conn.ReadPacket(link.SplitByUint16BE)
	return nil
}

const OutputFormat = "Send Count: %d, Recv Count: %d, Read Count: %d, Write Count: %d\n"

// This is an benchmark tool work with the echo_server.
//
// Start echo_server with 'bench' flag
//     go run echo_server.go -bench
//
// Start benchmark with echo_server address
//     go run echo_benchmark.go
//     go run echo_benchmark.go -num=100
//     go run echo_benchmark.go -size=1024
//     go run echo_benchmark.go -time=20
//     go run echo_benchmark.go -addr="127.0.0.1:10010"
func main() {
	flag.Parse()

	if MultiProcess() {
		return
	}

	var (
		msg       = Message(make([]byte, *messageSize))
		timeout   = time.Now().Add(time.Second * time.Duration(*runTime))
		initWait  = new(sync.WaitGroup)
		startChan = make(chan int)
		conns     = make([]*CountConn, 0, *clientNum)
	)

	for i := 0; i < *clientNum; i++ {
		initWait.Add(2)
		conn, err := net.DialTimeout("tcp", *serverAddr, time.Second*3)
		if err != nil {
			panic(err)
		}
		countConn := &CountConn{Conn: conn}
		conns = append(conns, countConn)
		go client(initWait, countConn, startChan, timeout, msg)
	}
	initWait.Wait()
	close(startChan)

	time.Sleep(time.Second * time.Duration(*runTime))
	var sum CountConn
	for i := 0; i < *clientNum; i++ {
		conn := conns[i]
		conn.Conn.Close()
		sum.SendCount += conn.SendCount
		sum.RecvCount += conn.RecvCount
		sum.ReadCount += conn.ReadCount
		sum.WriteCount += conn.WriteCount
	}
	fmt.Printf(OutputFormat, sum.SendCount, sum.RecvCount, sum.ReadCount, sum.WriteCount)
}

func client(initWait *sync.WaitGroup, conn *CountConn, startChan chan int, timeout time.Time, msg Message) {
	c := link.NewConn(conn, link.DefaultConfig.ConnConfig)
	client, _ := link.NewSession(0, c, link.DefaultConfig.SessionConfig)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		initWait.Done()
		<-startChan

		for {
			if err := client.Send(msg); err != nil {
				if timeout.After(time.Now()) {
					println("send error:", err.Error())
				}
				break
			}
			atomic.AddUint32(&conn.SendCount, 1)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		initWait.Done()
		<-startChan

		var msg Message
		for {
			if err := client.Receive(msg); err != nil {
				if timeout.After(time.Now()) {
					println("recv error:", err.Error())
				}
				break
			}
			atomic.AddUint32(&conn.RecvCount, 1)
		}
	}()

	wg.Wait()
}

type childProc struct {
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout bytes.Buffer
}

func MultiProcess() bool {
	if *proces <= 1 {
		if *waitMaster {
			fmt.Scanln()
		}
		return false
	}

	cmds := make([]*childProc, *proces)
	for i := 0; i < *proces; i++ {
		cmd := exec.Command(
			"go", "run", "echo_benchmark.go", "wait",
			"addr="+*serverAddr,
			"num="+strconv.Itoa(*clientNum / *proces),
			"size="+strconv.Itoa(*messageSize),
			"time="+strconv.Itoa(*runTime),
		)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			panic("get stdin pipe failed: " + err.Error())
		}
		cmds[i] = &childProc{Cmd: cmd, Stdin: stdin}
		cmd.Stdout = &cmds[i].Stdout
		cmd.Start()
	}

	for i := 0; i < *proces; i++ {
		cmds[i].Stdin.Write([]byte{'\n'})
	}

	for i := 0; i < *proces; i++ {
		err := cmds[i].Cmd.Wait()
		if err != nil {
			println("wait proc failed:", err.Error())
		}
	}

	var sum CountConn
	for i := 0; i < *proces; i++ {
		output := cmds[i].Stdout.String()
		fmt.Print(output)

		var c CountConn
		fmt.Sscanf(output, OutputFormat,
			&c.SendCount,
			&c.RecvCount,
			&c.ReadCount,
			&c.WriteCount,
		)

		sum.SendCount += c.SendCount
		sum.RecvCount += c.RecvCount
		sum.ReadCount += c.ReadCount
		sum.WriteCount += c.WriteCount
	}

	fmt.Println("--------------------")
	fmt.Printf(OutputFormat, sum.SendCount, sum.RecvCount, sum.ReadCount, sum.WriteCount)
	return true
}

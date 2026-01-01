package starlib

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"go.starlark.net/starlark"
)

type PIDCreatedCallback func(childPID, parentPID int)

func (rt *Runtime) pidAttrs() starlark.StringDict {
	return starlark.StringDict{
		"is_ancestor": starlark.NewBuiltin("pid.is_ancestor", rt.builtinPIDIsAncestor),
		"watch_new":   starlark.NewBuiltin("pid.watch_new", rt.builtinPIDWatchNew),
	}
}

func (rt *Runtime) builtinPIDIsAncestor(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var ancestorVal starlark.Int
	var descendantVal starlark.Int
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "ancestor", &ancestorVal, "descendant", &descendantVal); err != nil {
		return nil, err
	}

	ancestor, err := starlarkIntToInt(ancestorVal, "ancestor")
	if err != nil {
		return nil, err
	}
	descendant, err := starlarkIntToInt(descendantVal, "descendant")
	if err != nil {
		return nil, err
	}

	return starlark.Bool(IsAncestor(ancestor, descendant)), nil
}

func (rt *Runtime) builtinPIDWatchNew(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var cbVal starlark.Value
	var pollMsVal starlark.Int
	usePoll := false

	if err := starlark.UnpackArgs(
		b.Name(), args, kwargs,
		"callback", &cbVal,
		"poll_interval_ms?", &pollMsVal,
		"use_poll?", &usePoll,
	); err != nil {
		return nil, err
	}

	cb, ok := cbVal.(starlark.Callable)
	if !ok {
		return nil, fmt.Errorf("callback must be callable, got %s", cbVal.Type())
	}

	interval := 100 * time.Millisecond
	if pollMsVal.Sign() != 0 {
		ms, err := starlarkIntToInt(pollMsVal, "poll_interval_ms")
		if err != nil {
			return nil, err
		}
		if ms > 0 {
			interval = time.Duration(ms) * time.Millisecond
		}
	}

	ctx, cancel := context.WithCancel(rt.exec.ctx)
	done := make(chan struct{})

	callCB := func(childPID, parentPID int) {
		_, err := rt.call(thread, cb, starlark.Tuple{
			starlark.MakeInt(childPID),
			starlark.MakeInt(parentPID),
		}, nil)
		if err != nil {
			rt.logf("pid.watch_new callback error: %v", err)
		}
	}

	go func() {
		defer close(done)
		var err error
		if usePoll {
			err = WatchNewPIDsPoll(ctx, interval, callCB)
		} else {
			err = WatchNewPIDsNetlink(ctx, callCB)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			rt.logf("pid.watch_new error: %v", err)
		}
	}()

	stopFn := starlark.NewBuiltin("pid.watch_stop", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
			return nil, err
		}
		cancel()
		<-done
		return starlark.None, nil
	})

	return stopFn, nil
}

func starlarkIntToInt(v starlark.Int, name string) (int, error) {
	i64, ok := v.Int64()
	if !ok {
		return 0, fmt.Errorf("%s out of range", name)
	}
	maxInt := int64(int(^uint(0) >> 1))
	minInt := -maxInt - 1
	if i64 < minInt || i64 > maxInt {
		return 0, fmt.Errorf("%s out of range", name)
	}
	return int(i64), nil
}

func starlarkIntToInt64(v starlark.Int, name string) (int64, error) {
	i64, ok := v.Int64()
	if !ok {
		return 0, fmt.Errorf("%s out of range", name)
	}
	return i64, nil
}

func nativeByteOrder() binary.ByteOrder {
	var x uint16 = 0x1
	if *(*byte)(unsafe.Pointer(&x)) == 0x1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

var bo = nativeByteOrder()

// ReadPPID reads PPID from /proc/<pid>/stat.
func ReadPPID(pid int) (int, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid: %d", pid)
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	line := strings.TrimSpace(string(data))

	// Format: pid (comm) state ppid ...
	// comm can contain spaces, so find the last ')'
	rp := strings.LastIndexByte(line, ')')
	lp := strings.IndexByte(line, '(')
	if lp < 0 || rp < 0 || rp <= lp {
		return 0, fmt.Errorf("unexpected /proc/%d/stat format", pid)
	}

	rest := strings.TrimSpace(line[rp+1:])
	fields := strings.Fields(rest)
	// fields[0] = state, fields[1] = ppid
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected /proc/%d/stat fields", pid)
	}

	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("parse ppid: %w", err)
	}
	return ppid, nil
}

// IsAncestor returns true if ancestor == descendant, or ancestor is a parent/grandparent/... of descendant.
func IsAncestor(ancestor, descendant int) bool {
	if ancestor <= 0 || descendant <= 0 {
		return false
	}

	cur := descendant
	for {
		if cur == ancestor {
			return true
		}
		if cur == 1 || cur == 0 {
			return false
		}
		ppid, err := ReadPPID(cur)
		if err != nil || ppid <= 0 {
			return false // process exited or not accessible
		}
		cur = ppid
	}
}

// Netlink constants (Linux).
const (
	NETLINK_CONNECTOR = 11

	// From linux/connector.h and linux/cn_proc.h:
	CN_IDX_PROC = 1
	CN_VAL_PROC = 1

	// proc_cn_mcast_op
	PROC_CN_MCAST_LISTEN = 1
	PROC_CN_MCAST_IGNORE = 2

	// proc_event.what (bitmask values)
	PROC_EVENT_FORK = 0x00000001
)

// cn_msg header is 20 bytes:
// struct cb_id { u32 idx; u32 val; }
// struct cn_msg { cb_id id; u32 seq; u32 ack; u16 len; u16 flags; u8 data[0]; }
type cnMsgHeader struct {
	Idx   uint32
	Val   uint32
	Seq   uint32
	Ack   uint32
	Len   uint16
	Flags uint16
}

// proc_event fixed header is 16 bytes:
// u32 what; u32 cpu; u64 timestamp; union ...
type procEventHeader struct {
	What      uint32
	CPU       uint32
	Timestamp uint64
}

func makeProcCnMcastMsg(listen bool) ([]byte, error) {
	op := uint32(PROC_CN_MCAST_IGNORE)
	if listen {
		op = PROC_CN_MCAST_LISTEN
	}

	// Compose: nlmsghdr + cn_msg + op
	// nlmsghdr is 16 bytes in userspace ABI.
	const nlHdrLen = 16
	const cnHdrLen = 20
	const opLen = 4
	totalLen := nlHdrLen + cnHdrLen + opLen

	buf := bytes.NewBuffer(make([]byte, 0, totalLen))

	// nlmsghdr
	// struct nlmsghdr { u32 len; u16 type; u16 flags; u32 seq; u32 pid; }
	// Using NLMSG_DONE (= 0x3) for type (common in examples).
	var nlType uint16 = 0x3 // NLMSG_DONE
	var nlFlags uint16
	var nlSeq uint32
	nlPid := uint32(os.Getpid())

	if err := binary.Write(buf, bo, uint32(totalLen)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, bo, nlType); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, bo, nlFlags); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, bo, nlSeq); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, bo, nlPid); err != nil {
		return nil, err
	}

	// cn_msg header
	cn := cnMsgHeader{
		Idx:   CN_IDX_PROC,
		Val:   CN_VAL_PROC,
		Seq:   0,
		Ack:   0,
		Len:   uint16(opLen),
		Flags: 0,
	}
	if err := binary.Write(buf, bo, cn); err != nil {
		return nil, err
	}

	// payload: proc_cn_mcast_op (u32)
	if err := binary.Write(buf, bo, op); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func newProcConnectorSocket() (int, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM, NETLINK_CONNECTOR)
	if err != nil {
		return -1, fmt.Errorf("socket: %w", err)
	}
	_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4*1024*1024)

	// Bind to our PID and the PROC connector group.
	sa := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Pid:    0,
		Groups: CN_IDX_PROC,
	}
	if err := syscall.Bind(fd, sa); err != nil {
		_ = syscall.Close(fd)
		return -1, fmt.Errorf("bind: %w", err)
	}

	subMsg, err := makeProcCnMcastMsg(true)
	if err != nil {
		_ = syscall.Close(fd)
		return -1, err
	}
	if err := syscall.Sendto(fd, subMsg, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		_ = syscall.Close(fd)
		return -1, fmt.Errorf("subscribe sendto: %w", err)
	}

	return fd, nil
}

func closeProcConnectorSocket(fd int) {
	unsubMsg, _ := makeProcCnMcastMsg(false)
	_ = syscall.Sendto(fd, unsubMsg, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK})
	_ = syscall.Close(fd)
}

// WatchNewPIDsNetlink listens for PROC_EVENT_FORK and calls cb(childPID, parentPID).
// Stop by cancelling ctx.
func WatchNewPIDsNetlink(ctx context.Context, cb PIDCreatedCallback) error {
	if cb == nil {
		return errors.New("nil callback")
	}

	fd, err := newProcConnectorSocket()
	if err != nil {
		return err
	}
	defer closeProcConnectorSocket(fd)

	return watchNewPIDsNetlinkLoop(ctx, fd, cb)
}

func watchNewPIDsNetlinkLoop(ctx context.Context, fd int, cb PIDCreatedCallback) error {
	buf := make([]byte, 4096)

	for {
		// Cooperative cancel: use a short recv timeout.
		_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &syscall.Timeval{Sec: 0, Usec: 250000})

		select {
		case <-ctx.Done():
			return nil
		default:
		}

		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			// timeout -> loop to check ctx
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				continue
			}
			if err == syscall.EINTR {
				continue
			}
			if err == syscall.ENOBUFS {
				// Netlink socket buffer overflow; drop this burst and keep listening.
				continue
			}
			return fmt.Errorf("recvfrom: %w", err)
		}

		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			// ignore malformed bursts
			continue
		}

		for _, m := range msgs {
			// m.Data should start with cn_msg header
			if len(m.Data) < 20 {
				continue
			}

			var cn cnMsgHeader
			if err := binary.Read(bytes.NewReader(m.Data[:20]), bo, &cn); err != nil {
				continue
			}
			if cn.Idx != CN_IDX_PROC || cn.Val != CN_VAL_PROC {
				continue
			}

			// cn.Len says how many bytes of data follow the header.
			payload := m.Data[20:]
			if int(cn.Len) > len(payload) || len(payload) < 16 {
				continue
			}

			// proc_event header
			var evh procEventHeader
			if err := binary.Read(bytes.NewReader(payload[:16]), bo, &evh); err != nil {
				continue
			}

			if evh.What != PROC_EVENT_FORK {
				continue
			}

			// fork event payload:
			// parent_pid u32, parent_tgid u32, child_pid u32, child_tgid u32
			if len(payload) < 16+16 {
				continue
			}
			forkData := payload[16 : 16+16]
			parentPID := int(bo.Uint32(forkData[0:4]))
			childPID := int(bo.Uint32(forkData[8:12]))

			cb(childPID, parentPID)
		}
	}
}

func snapshotProcPIDs() ([]int, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, 4096)
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		ok := true
		for i := 0; i < len(name); i++ {
			if name[i] < '0' || name[i] > '9' {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		v, err := strconv.Atoi(name)
		if err == nil && v > 0 {
			pids = append(pids, v)
		}
	}
	sort.Ints(pids)
	return pids, nil
}

// WatchNewPIDsPoll polls /proc and calls cb(pid, ppid) for newly appearing PIDs.
// Can miss very short-lived processes between polls.
func WatchNewPIDsPoll(ctx context.Context, interval time.Duration, cb PIDCreatedCallback) error {
	if cb == nil {
		return errors.New("nil callback")
	}
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}

	prev, err := snapshotProcPIDs()
	if err != nil {
		return err
	}

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			cur, err := snapshotProcPIDs()
			if err != nil {
				return err
			}

			// diff: cur - prev (two-pointer)
			i, j := 0, 0
			for i < len(cur) {
				if j >= len(prev) || cur[i] < prev[j] {
					newPID := cur[i]
					ppid, _ := ReadPPID(newPID) // might fail if exited
					cb(newPID, ppid)
					i++
				} else if cur[i] > prev[j] {
					j++
				} else {
					i++
					j++
				}
			}
			prev = cur
		}
	}
}

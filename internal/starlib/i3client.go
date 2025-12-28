package starlib

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	i3ipc "github.com/mdirkse/i3ipc-go"
)

// I3Client is a small, dedicated i3 IPC request/reply client.
//
// Motivation: some i3 IPC Go libraries multiplex event subscriptions and command
// requests on a shared connection. Under load this can corrupt framing and cause:
//   - "Invalid magic string" (header desync)
//   - JSON decode failures with embedded NULs
//   - broken pipe on write
//
// i3d uses i3ipc-go only for event subscription; all request/reply traffic goes
// through this dedicated client/connection.
type I3Client struct {
	debug  bool
	debugf func(string, ...any)

	mu       sync.Mutex
	sockPath string
	conn     net.Conn
}

func NewI3Client(debug bool, debugf func(string, ...any)) (*I3Client, error) {
	path, err := discoverI3SocketPath(context.Background(), debug, debugf)
	if err != nil {
		return nil, err
	}
	c := &I3Client{
		debug:    debug,
		debugf:   debugf,
		sockPath: path,
	}
	if debug && debugf != nil {
		debugf("i3 IPC client: socket=%s", path)
	}
	return c, nil
}

func (c *I3Client) SocketPath() string {
	return c.sockPath
}

func (c *I3Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *I3Client) Command(cmd string) (bool, error) {
	payload, err := c.roundTrip(uint32(0), []byte(cmd))
	if err != nil {
		return false, err
	}

	// i3 COMMAND reply: array of objects with at least {"success": bool}.
	var res []struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(payload, &res); err != nil {
		return false, fmt.Errorf("command reply json decode: %w", err)
	}
	if len(res) == 0 {
		// Treat empty response as failure (unexpected).
		return false, fmt.Errorf("command reply: empty response")
	}
	for _, it := range res {
		if !it.Success {
			return false, nil
		}
	}
	return true, nil
}

func (c *I3Client) Raw(mt i3ipc.MessageType, payload string) ([]byte, error) {
	return c.roundTrip(uint32(mt), []byte(payload))
}

func (c *I3Client) GetMarks() ([]string, error) {
	raw, err := c.roundTrip(uint32(i3ipc.I3GetMarks), nil)
	if err != nil {
		return nil, err
	}
	var marks []string
	if err := json.Unmarshal(raw, &marks); err != nil {
		return nil, fmt.Errorf("get_marks reply json decode: %w", err)
	}
	return marks, nil
}

func (c *I3Client) GetBarIds() ([]string, error) {
	// i3 returns bar IDs via GET_BAR_CONFIG with empty payload.
	raw, err := c.roundTrip(uint32(i3ipc.I3GetBarConfig), nil)
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("get_bar_ids reply json decode: %w", err)
	}
	return ids, nil
}

const i3Magic = "i3-ipc"

func (c *I3Client) roundTrip(msgType uint32, payload []byte) ([]byte, error) {
	// Single in-flight request per client (simple + keeps framing safe).
	c.mu.Lock()
	defer c.mu.Unlock()

	// Try once, then reconnect+retry once for broken connections / desync.
	raw, err := c.roundTripLocked(msgType, payload)
	if err == nil {
		return raw, nil
	}

	if c.debug && c.debugf != nil {
		c.debugf("i3 IPC roundTrip error: %v", err)
	}

	// For connection-level problems (or framing desync), reconnect and retry once.
	if !isRecoverableIPCError(err) {
		return nil, err
	}

	if c.debug && c.debugf != nil {
		c.debugf("i3 IPC reconnecting and retrying once")
	}
	_ = c.closeLocked()
	if err2 := c.ensureConnLocked(); err2 != nil {
		return nil, err // original error more relevant
	}

	raw2, err2 := c.roundTripLocked(msgType, payload)
	if err2 != nil {
		return nil, err2
	}
	return raw2, nil
}

func (c *I3Client) roundTripLocked(msgType uint32, payload []byte) ([]byte, error) {
	if err := c.ensureConnLocked(); err != nil {
		return nil, err
	}

	if c.debug && c.debugf != nil {
		c.debugf("i3 IPC -> type=%d payload_len=%d", msgType, len(payload))
	}

	// Deadline keeps a stuck socket from hanging the daemon forever.
	_ = c.conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Write request.
	hdr := make([]byte, 6+4+4)
	copy(hdr[:6], []byte(i3Magic))
	binary.LittleEndian.PutUint32(hdr[6:10], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[10:14], msgType)

	if err := writeFull(c.conn, hdr); err != nil {
		return nil, fmt.Errorf("i3 IPC write header: %w", err)
	}
	if len(payload) != 0 {
		if err := writeFull(c.conn, payload); err != nil {
			return nil, fmt.Errorf("i3 IPC write payload: %w", err)
		}
	}

	// Read response header.
	rhdr := make([]byte, 6+4+4)
	if _, err := io.ReadFull(c.conn, rhdr); err != nil {
		return nil, fmt.Errorf("i3 IPC read header: %w", err)
	}
	if string(rhdr[:6]) != i3Magic {
		// Include a short, printable preview for debugging.
		prev := sanitizePreview(rhdr[:6])
		return nil, fmt.Errorf("i3 IPC framing desync: invalid magic %q (want %q)", prev, i3Magic)
	}
	plen := binary.LittleEndian.Uint32(rhdr[6:10])
	rtype := binary.LittleEndian.Uint32(rhdr[10:14])

	if c.debug && c.debugf != nil {
		c.debugf("i3 IPC <- type=%d payload_len=%d", rtype, plen)
	}

	if plen > 128*1024*1024 {
		return nil, fmt.Errorf("i3 IPC reply too large: %d bytes", plen)
	}

	out := make([]byte, int(plen))
	if plen != 0 {
		if _, err := io.ReadFull(c.conn, out); err != nil {
			return nil, fmt.Errorf("i3 IPC read payload: %w", err)
		}
	}

	return out, nil
}

func (c *I3Client) ensureConnLocked() error {
	if c.conn != nil {
		return nil
	}
	conn, err := net.DialTimeout("unix", c.sockPath, 400*time.Millisecond)
	if err != nil {
		return fmt.Errorf("dial i3 IPC socket %s: %w", c.sockPath, err)
	}
	c.conn = conn
	return nil
}

func (c *I3Client) closeLocked() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

func sanitizePreview(b []byte) string {
	// Replace non-printable bytes to keep logs readable.
	var buf bytes.Buffer
	for _, c := range b {
		if c >= 32 && c <= 126 {
			_ = buf.WriteByte(c)
		} else {
			_, _ = buf.WriteString(fmt.Sprintf("\\x%02x", c))
		}
	}
	return buf.String()
}

func isRecoverableIPCError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ENOTCONN) {
		return true
	}
	var ne *net.OpError
	if errors.As(err, &ne) {
		// Most net.OpError are recoverable by reconnecting.
		return true
	}
	// Framing desync should be fixed by reconnecting.
	if strings.Contains(err.Error(), "framing desync") || strings.Contains(err.Error(), "invalid magic") {
		return true
	}
	return false
}

func discoverI3SocketPath(ctx context.Context, debug bool, debugf func(string, ...any)) (string, error) {
	cands := discoverI3SocketCandidates(ctx)

	// Stable ordering for logs/debug.
	sort.Strings(cands)

	if debug && debugf != nil {
		debugf("i3 IPC discovery: candidates=%d", len(cands))
		if len(cands) != 0 {
			debugf("i3 IPC discovery: %v", cands)
		}
	}

	for _, p := range cands {
		conn, err := net.DialTimeout("unix", p, 200*time.Millisecond)
		if err != nil {
			if debug && debugf != nil {
				debugf("i3 IPC discovery: dial failed %s: %v", p, err)
			}
			continue
		}
		_ = conn.Close()
		return p, nil
	}

	if len(cands) == 0 {
		return "", fmt.Errorf("could not find i3 IPC socket (no candidates)")
	}
	return "", fmt.Errorf("could not connect to any i3 IPC socket candidate (tried=%d)", len(cands))
}

func discoverI3SocketCandidates(ctx context.Context) []string {
	seen := map[string]struct{}{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
	}

	// If launched by i3, this is commonly present.
	add(os.Getenv("I3SOCK"))

	// XDG_RUNTIME_DIR default location.
	if rd := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); rd != "" {
		paths, _ := filepath.Glob(filepath.Join(rd, "i3", "ipc-socket.*"))
		for _, p := range paths {
			add(p)
		}
	}

	// Common fallback: /run/user/$UID (covers non-systemd launch env too).
	if uid := os.Getuid(); uid > 0 {
		paths, _ := filepath.Glob(filepath.Join("/run/user", fmt.Sprintf("%d", uid), "i3", "ipc-socket.*"))
		for _, p := range paths {
			add(p)
		}
	}

	// Last resort: ask i3 itself.
	// Keep it short and safe; ignore failures.
	{
		cctx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
		defer cancel()

		cmd := exec.CommandContext(cctx, "i3", "--get-socketpath")
		out, err := cmd.Output()
		if err == nil {
			add(string(bytes.TrimSpace(out)))
		}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out
}

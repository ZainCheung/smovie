package hongguo

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Progress is how far a download is.
type Progress struct {
	Received, Total int64
	// Stream tells that dst grows in place to Total bytes and plays while
	// it does: the media's index comes before its samples, and each
	// sample is decrypted as it arrives.
	Stream bool
	// Ready is the bytes at the start of dst that are final, while Stream.
	Ready int64
	// Decrypting tells that all is in and ffmpeg decrypts it, when the
	// media cannot stream.
	Decrypting bool
}

// ErrMediaUnavailable means a CDN rejected an address before any video bytes
// were published. The caller may resolve the episode once more for fresh URLs.
// A 403 alone cannot distinguish expiry from a temporary CDN rejection.
var ErrMediaUnavailable = errors.New("片源地址暂不可用")

type mediaStatusError int

func (e mediaStatusError) Error() string {
	return fmt.Sprintf("视频下载失败（HTTP %d）", e)
}

func (e mediaStatusError) Is(target error) bool {
	if target != ErrMediaUnavailable {
		return false
	}
	switch int(e) {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return true
	}
	return false
}

const maxMediaURLs = 3

// openMedia tries the rendition's primary and backup CDNs, bounded to three
// distinct URLs. Recovery ends once a body is accepted: never splice streams
// after publishing bytes to a growing file the player may already have open.
func openMedia(ctx context.Context, info *PlayInfo) (*http.Response, error) {
	var lastErr error
	seen := map[string]bool{}
	for _, raw := range append([]string{info.URL}, info.BackupURLs...) {
		if raw == "" || seen[raw] {
			continue
		}
		if len(seen) == maxMediaURLs {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seen[raw] = true
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", info.UserAgent)
		res, err := mediaClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("视频下载失败：%w", err)
			continue
		}
		if res.StatusCode == http.StatusOK {
			return res, nil
		}
		res.Body.Close()
		lastErr = mediaStatusError(res.StatusCode)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lastErr == nil {
		lastErr = errors.New("接口未返回本集片源地址")
	}
	return nil, lastErr
}

// Download saves an episode's media, decrypted, to dst, telling progress
// as it goes. When it can, it writes dst in place from its start, so that
// a player reads the episode while it downloads; otherwise dst appears
// whole at the end.
func Download(ctx context.Context, info *PlayInfo, dst string, progress func(Progress)) error {
	if progress == nil {
		progress = func(Progress) {}
	}
	res, err := openMedia(ctx, info)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	total := res.ContentLength
	var key []byte
	if info.ContentKey != "" {
		if key, err = hex.DecodeString(info.ContentKey); err != nil || len(key) != 16 {
			return errors.New("片源密钥无效")
		}
	}
	head, mdatAt, herr := readHead(res.Body)
	if herr != nil && !errors.Is(herr, errNoStream) {
		return fmt.Errorf("视频下载中断：%w", herr)
	}
	if herr == nil && total > 0 {
		if p, err := planStream(head[:mdatAt], key, total); err == nil {
			return p.write(res.Body, head, mdatAt, total, dst, progress)
		}
	}
	return downloadWhole(ctx, io.MultiReader(bytes.NewReader(head), res.Body), int64(len(head)), total, info, dst, progress)
}

var errNoStream = errors.New("media cannot stream")

// readHead reads the top-level boxes before the media data, returning
// what it read, which may run into the data, and where the data's box
// starts. It returns errNoStream with what it read when the index does
// not come first.
func readHead(r io.Reader) ([]byte, int, error) {
	const limit = 32 << 20
	var head []byte
	need := func(n int) error {
		for len(head) < n {
			if n > limit {
				return errNoStream
			}
			buf := make([]byte, max(n-len(head), 64<<10))
			m, err := r.Read(buf)
			head = append(head, buf[:m]...)
			if err == io.EOF {
				return errNoStream
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	moov := false
	for at := 0; ; {
		if err := need(at + 16); err != nil {
			return head, 0, err
		}
		size, typ := int64(binary.BigEndian.Uint32(head[at:])), string(head[at+4:at+8])
		if size == 1 {
			size = int64(binary.BigEndian.Uint64(head[at+8:]))
		}
		if typ == "mdat" {
			if !moov {
				return head, 0, errNoStream
			}
			return head, at, nil
		}
		if typ == "moof" || size < 8 || size > limit {
			return head, 0, errNoStream
		}
		moov = moov || typ == "moov"
		at += int(size)
	}
}

// downloadWhole saves all of r, of which received bytes were read, and
// decrypts it with ffmpeg once it is in.
func downloadWhole(ctx context.Context, r io.Reader, received, total int64, info *PlayInfo, dst string, progress func(Progress)) error {
	tmp, err := os.MkdirTemp(filepath.Dir(dst), ".dl-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	encrypted := filepath.Join(tmp, "source.mp4")
	f, err := os.Create(encrypted)
	if err != nil {
		return err
	}
	received = 0
	buf := make([]byte, 256<<10)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			received += int64(n)
			progress(Progress{Received: received, Total: max(total, 0)})
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return fmt.Errorf("视频下载中断：%w", rerr)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if total > 0 && received != total {
		return errors.New("视频下载不完整")
	}
	out := filepath.Join(tmp, "out.mp4")
	if info.ContentKey == "" {
		out = encrypted
	} else {
		progress(Progress{Received: received, Total: received, Decrypting: true})
		ff := FFmpeg()
		if ff == "" {
			return errors.New("未找到 ffmpeg，无法解密片源。请先安装：brew install ffmpeg")
		}
		cmd := exec.CommandContext(ctx, ff, "-y", "-hide_banner", "-loglevel", "error",
			"-decryption_key", info.ContentKey, "-i", encrypted, "-map", "0:v:0", "-map", "0:a?",
			"-c", "copy", "-tag:v", videoTag(info.Codec), "-movflags", "+faststart", "-f", "mp4", out)
		if msg, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("片源解密失败：%v %s", err, strings.TrimSpace(string(msg)))
		}
	}
	return os.Rename(out, dst)
}

// videoTag names the sample entry WebKit plays: hvc1 rather than hev1.
func videoTag(codec string) string {
	if codec == "hevc" || codec == "h265" || codec == "hvc1" || codec == "hev1" {
		return "hvc1"
	}
	return "avc1"
}

// streamPlan is how to turn an encrypted MP4 whose index comes first into
// a plain one of the same size, sample by sample: the index, rewritten to
// name the codecs rather than the encryption, and the samples decrypted
// in place.
type streamPlan struct {
	header  []byte // the boxes before the media data, rewritten
	block   cipher.Block
	samples []encSample // by offset
}

type encSample struct {
	off, size int64
	iv        [16]byte
	subs      []subsample // none: all of it is encrypted
}

type subsample struct{ clear, enc int }

// write writes dst from the head read and the rest of body.
func (p *streamPlan) write(body io.Reader, head []byte, mdatAt int, total int64, dst string, progress func(Progress)) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(p.header); err != nil {
		return err
	}
	pend := append([]byte(nil), head[mdatAt:]...) // read, not yet written
	pendAt := int64(mdatAt)
	received := int64(len(head))
	next := 0
	// flush decrypts the samples pend holds whole, and writes up to the
	// first it does not.
	flush := func() error {
		end := pendAt + int64(len(pend))
		for next < len(p.samples) && p.samples[next].off+p.samples[next].size <= end {
			s := &p.samples[next]
			s.decrypt(p.block, pend[s.off-pendAt:s.off-pendAt+s.size])
			next++
		}
		upto := end
		if next < len(p.samples) {
			upto = min(upto, p.samples[next].off)
		}
		if n := upto - pendAt; n > 0 {
			if _, err := f.Write(pend[:n]); err != nil {
				return err
			}
			pend = append(pend[:0], pend[n:]...)
			pendAt = upto
		}
		return nil
	}
	if err := flush(); err != nil {
		return err
	}
	progress(Progress{Received: received, Total: total, Stream: true, Ready: pendAt})
	buf := make([]byte, 256<<10)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			received += int64(n)
			if received > total {
				return errors.New("视频下载出错：大小不符")
			}
			pend = append(pend, buf[:n]...)
			if err := flush(); err != nil {
				return err
			}
			progress(Progress{Received: received, Total: total, Stream: true, Ready: pendAt})
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("视频下载中断：%w", rerr)
		}
	}
	if received != total || next < len(p.samples) || len(pend) > 0 {
		return errors.New("视频下载不完整")
	}
	return f.Close()
}

func (s *encSample) decrypt(block cipher.Block, b []byte) {
	ctr := cipher.NewCTR(block, s.iv[:])
	if len(s.subs) == 0 {
		ctr.XORKeyStream(b, b)
		return
	}
	at := 0
	for _, sub := range s.subs {
		at += sub.clear
		ctr.XORKeyStream(b[at:at+sub.enc], b[at:at+sub.enc])
		at += sub.enc
	}
}

// mp4Box is a box in a buffer: its type, and where it, its content and
// its end are.
type mp4Box struct {
	typ              string
	start, body, end int
}

func mp4Boxes(b []byte, from, to int) ([]mp4Box, error) {
	var out []mp4Box
	for at := from; at < to; {
		if to-at < 8 {
			return nil, errors.New("short box")
		}
		size, hdr := int(binary.BigEndian.Uint32(b[at:])), 8
		if size == 1 {
			if to-at < 16 {
				return nil, errors.New("short box")
			}
			size, hdr = int(binary.BigEndian.Uint64(b[at+8:])), 16
		} else if size == 0 {
			size = to - at
		}
		if size < hdr || size > to-at {
			return nil, errors.New("bad box size")
		}
		out = append(out, mp4Box{string(b[at+4 : at+8]), at, at + hdr, at + size})
		at += size
	}
	return out, nil
}

func child(b []byte, parent mp4Box, typ string) (mp4Box, bool) {
	kids, err := mp4Boxes(b, parent.body, parent.end)
	if err != nil {
		return mp4Box{}, false
	}
	for _, k := range kids {
		if k.typ == typ {
			return k, true
		}
	}
	return mp4Box{}, false
}

// rename gives a box another type, which keeps every offset.
func rename(b []byte, x mp4Box, typ string) { copy(b[x.start+4:x.start+8], typ) }

// planStream reads the boxes before the media data. It fails for what it
// does not know how to decrypt in place, which ffmpeg then decrypts.
func planStream(header, key []byte, total int64) (*streamPlan, error) {
	h := append([]byte(nil), header...)
	top, err := mp4Boxes(h, 0, len(h))
	if err != nil {
		return nil, err
	}
	p := &streamPlan{header: h}
	if key != nil {
		if p.block, err = aes.NewCipher(key); err != nil {
			return nil, err
		}
	}
	for _, t := range top {
		if t.typ != "moov" {
			continue
		}
		kids, err := mp4Boxes(h, t.body, t.end)
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			switch k.typ {
			case "mvex":
				return nil, errors.New("fragmented")
			case "pssh":
				rename(h, k, "free")
			case "trak":
				s, err := planTrack(h, k, key != nil)
				if err != nil {
					return nil, err
				}
				p.samples = append(p.samples, s...)
			}
		}
	}
	sort.Slice(p.samples, func(i, j int) bool { return p.samples[i].off < p.samples[j].off })
	end := int64(len(h)) + 8
	for _, s := range p.samples {
		if s.off < end || s.off+s.size > total {
			return nil, errors.New("samples out of place")
		}
		end = s.off + s.size
	}
	return p, nil
}

func planTrack(h []byte, trak mp4Box, haveKey bool) ([]encSample, error) {
	stbl, ok := trak, true
	for _, name := range []string{"mdia", "minf", "stbl"} {
		if stbl, ok = child(h, stbl, name); !ok {
			return nil, nil // no samples
		}
	}
	kids, err := mp4Boxes(h, stbl.body, stbl.end)
	if err != nil {
		return nil, err
	}
	box := map[string]mp4Box{}
	for _, k := range kids {
		box[k.typ] = k
	}
	stsd, ok := box["stsd"]
	if !ok || stsd.end-stsd.body < 8 {
		return nil, errors.New("no stsd")
	}
	if binary.BigEndian.Uint32(h[stsd.body+4:]) != 1 {
		return nil, errors.New("several sample entries")
	}
	entries, err := mp4Boxes(h, stsd.body+8, stsd.end)
	if err != nil || len(entries) != 1 {
		return nil, errors.New("bad stsd")
	}
	entry := entries[0]
	var skip int
	switch entry.typ {
	case "encv":
		skip = 78
	case "enca":
		skip = 28
		if entry.end-entry.body >= 10 {
			switch binary.BigEndian.Uint16(h[entry.body+8:]) { // sound entry version
			case 1:
				skip += 16
			case 2:
				skip += 36
			}
		}
	default:
		return nil, nil // in the clear
	}
	if entry.end-entry.body < skip {
		return nil, errors.New("short sample entry")
	}
	inner, err := mp4Boxes(h, entry.body+skip, entry.end)
	if err != nil {
		return nil, err
	}
	var sinf mp4Box
	for _, k := range inner {
		if k.typ == "sinf" {
			sinf = k
		}
	}
	frma, ok1 := child(h, sinf, "frma")
	schm, ok2 := child(h, sinf, "schm")
	schi, ok3 := child(h, sinf, "schi")
	if sinf.typ == "" || !ok1 || !ok2 || !ok3 || frma.end-frma.body < 4 || schm.end-schm.body < 8 {
		return nil, errors.New("no protection scheme")
	}
	if string(h[schm.body+4:schm.body+8]) != "cenc" {
		return nil, errors.New("scheme is not cenc")
	}
	tenc, ok := child(h, schi, "tenc")
	if !ok || tenc.end-tenc.body < 24 {
		return nil, errors.New("no tenc")
	}
	protected, ivSize := h[tenc.body+6], int(h[tenc.body+7])
	format := string(h[frma.body : frma.body+4])
	if format == "hev1" {
		format = "hvc1"
	}
	rename(h, entry, format)
	rename(h, sinf, "free")
	for _, name := range []string{"senc", "saiz", "saio"} {
		if b, ok := box[name]; ok {
			rename(h, b, "free")
		}
	}
	for _, name := range []string{"sgpd", "sbgp"} {
		if b, ok := box[name]; ok && b.end-b.body >= 8 && string(h[b.body+4:b.body+8]) == "seig" {
			return nil, errors.New("sample groups change the key")
		}
	}
	if protected == 0 {
		return nil, nil
	}
	if !haveKey {
		return nil, errors.New("encrypted without a key")
	}
	if ivSize != 8 && ivSize != 16 {
		return nil, errors.New("unsupported IV size")
	}
	offs, sizes, err := sampleLayout(h, box)
	if err != nil {
		return nil, err
	}
	senc, ok := box["senc"]
	if !ok {
		return nil, errors.New("no senc")
	}
	return parseSenc(h[senc.body:senc.end], ivSize, offs, sizes)
}

// sampleLayout returns where each sample of a track is and its size.
func sampleLayout(h []byte, box map[string]mp4Box) (offs, sizes []int64, err error) {
	bad := errors.New("bad sample table")
	full := func(name string, min int) ([]byte, bool) {
		b, ok := box[name]
		if !ok || b.end-b.body < min {
			return nil, false
		}
		return h[b.body+4 : b.end], true // past version and flags
	}
	stsz, ok := full("stsz", 12)
	if !ok {
		return nil, nil, bad
	}
	fixed, count := int64(binary.BigEndian.Uint32(stsz)), int(binary.BigEndian.Uint32(stsz[4:]))
	if fixed == 0 && len(stsz) < 8+4*count {
		return nil, nil, bad
	}
	sizes = make([]int64, count)
	for i := range sizes {
		if sizes[i] = fixed; fixed == 0 {
			sizes[i] = int64(binary.BigEndian.Uint32(stsz[8+4*i:]))
		}
	}
	var chunks []int64
	if co, ok := full("stco", 8); ok {
		n := int(binary.BigEndian.Uint32(co))
		if len(co) < 4+4*n {
			return nil, nil, bad
		}
		for i := range n {
			chunks = append(chunks, int64(binary.BigEndian.Uint32(co[4+4*i:])))
		}
	} else if co, ok := full("co64", 8); ok {
		n := int(binary.BigEndian.Uint32(co))
		if len(co) < 4+8*n {
			return nil, nil, bad
		}
		for i := range n {
			chunks = append(chunks, int64(binary.BigEndian.Uint64(co[4+8*i:])))
		}
	} else {
		return nil, nil, bad
	}
	stsc, ok := full("stsc", 8)
	if !ok {
		return nil, nil, bad
	}
	n := int(binary.BigEndian.Uint32(stsc))
	if n == 0 || len(stsc) < 4+12*n {
		return nil, nil, bad
	}
	offs = make([]int64, 0, count)
	for e := range n {
		first := int(binary.BigEndian.Uint32(stsc[4+12*e:]))
		per := int(binary.BigEndian.Uint32(stsc[8+12*e:]))
		last := len(chunks) + 1
		if e+1 < n {
			last = int(binary.BigEndian.Uint32(stsc[4+12*(e+1):]))
		}
		if first < 1 || last > len(chunks)+1 || first > last {
			return nil, nil, bad
		}
		for c := first; c < last; c++ {
			at := chunks[c-1]
			for range per {
				if len(offs) == count {
					return nil, nil, bad
				}
				offs = append(offs, at)
				at += sizes[len(offs)-1]
			}
		}
	}
	if len(offs) != count {
		return nil, nil, bad
	}
	return offs, sizes, nil
}

// parseSenc reads each sample's IV and subsamples.
func parseSenc(b []byte, ivSize int, offs, sizes []int64) ([]encSample, error) {
	bad := errors.New("bad senc")
	if len(b) < 8 {
		return nil, bad
	}
	flags := binary.BigEndian.Uint32(b) & 0xffffff
	if int(binary.BigEndian.Uint32(b[4:])) != len(offs) {
		return nil, bad
	}
	at := 8
	out := make([]encSample, len(offs))
	for i := range out {
		s := &out[i]
		s.off, s.size = offs[i], sizes[i]
		if len(b) < at+ivSize {
			return nil, bad
		}
		copy(s.iv[:], b[at:at+ivSize])
		at += ivSize
		if flags&2 == 0 {
			continue
		}
		if len(b) < at+2 {
			return nil, bad
		}
		n := int(binary.BigEndian.Uint16(b[at:]))
		at += 2
		if len(b) < at+6*n {
			return nil, bad
		}
		var sum int64
		for range n {
			sub := subsample{int(binary.BigEndian.Uint16(b[at:])), int(binary.BigEndian.Uint32(b[at+2:]))}
			s.subs = append(s.subs, sub)
			sum += int64(sub.clear) + int64(sub.enc)
			at += 6
		}
		if sum != s.size {
			return nil, bad
		}
	}
	return out, nil
}

// SPDX-License-Identifier: GPL-3.0-only
// Playback signing ported from jackotom/hongguo-drama/src/native/app-source.js.
// Its SDK algorithms originate in woshishiq1/drpys, commit
// 22261adfa31435e3b3ef5a730b8a2a75bcaf1715. Tables are protocol constants.
package hongguo

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var signingStarted = time.Now().UnixMilli()
var signingNonce = func() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}()

func random32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return binary.LittleEndian.Uint32(b[:])
}
func le32(v uint32) []byte        { return binary.LittleEndian.AppendUint32(nil, v) }
func be32(v uint32) []byte        { return binary.BigEndian.AppendUint32(nil, v) }
func unhex(s string) []byte       { b, _ := hex.DecodeString(s); return b }
func join(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// signedRequest is a POST of payload to an app API, signed as playback's.
func signedRequest(ctx context.Context, api string, payload []byte, dev *device, now time.Time) (*http.Request, error) {
	return signedAPIRequest(ctx, http.MethodPost, api, nil, payload, dev, now)
}

// The signed bytes must be exactly the bytes sent in the URL and POST body.
// Search ticket values until Medusa uses the supported branch, as upstream does.
func signedAPIRequest(ctx context.Context, method, api string, params url.Values, payload []byte, dev *device, now time.Time) (*http.Request, error) {
	q := url.Values{}
	for k, v := range deviceQuery {
		q.Set(k, v)
	}
	q.Set("device_id", dev.DeviceID)
	q.Set("iid", dev.IID)
	q.Set("cdid", dev.CDID)
	for k, values := range params {
		q[k] = append([]string(nil), values...)
	}
	ts := uint32(now.Unix())
	q.Set("ts", strconv.FormatUint(uint64(ts), 10))
	for n := 0; n < 128; n++ {
		ticket := strconv.FormatInt(now.UnixMilli()+int64(n), 10)
		q.Set("_rticket", ticket)
		query := strings.ReplaceAll(q.Encode(), "+", "%20")
		queryHash := sm3([]byte(query))
		bodyHash := signingBodyHash(payload)
		if signingBranch(queryHash, bodyHash[:], ts) != 0 {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, method, api+"?"+query, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		for k, v := range map[string]string{
			"User-Agent": playbackUA, "Accept": "application/json; charset=utf-8,application/x-protobuf",
			"Content-Type": "application/json; charset=UTF-8", "x-ss-req-ticket": ticket,
			"sdk-version": "2", "x-xs-from-web": "0", "x-khronos": strconv.FormatUint(uint64(ts), 10),
			"x-gorgon": xGorgon(query, payload, ts, uint16(random32())),
			"x-ladon":  base64.StdEncoding.EncodeToString(be32(ts)),
			"x-argus":  base64.StdEncoding.EncodeToString(le32(ts)),
			"x-helios": helios(ts, random32()),
			"x-medusa": buildMedusa(query, payload, ts, dev, signingNonce, signingStarted, random32, q.Get("version_name")),
			"x-tt-dt":  "", "x-tt-store-region": "cn", "x-tt-store-region-src": "did",
		} {
			req.Header.Set(k, v)
		}
		if payload != nil {
			req.Header.Set("x-ss-stub", strings.ToUpper(hex.EncodeToString(bodyHash[:])))
		} else {
			req.Header.Del("Content-Type")
			req.Header.Set("x-tt-request-tag", "t=0;n=0")
			req.Header.Set("passport-sdk-version", "50561")
		}
		if dev.Cookie != "" {
			req.Header.Set("Cookie", dev.Cookie)
		}
		return req, nil
	}
	return nil, errors.New("无法生成播放请求签名")
}

// GET has no body: the SDK signs sixteen zero bytes, not MD5 of an empty body.
func signingBodyHash(body []byte) [16]byte {
	if body == nil {
		return [16]byte{}
	}
	return md5.Sum(body)
}

func signingIV(v uint32, data []byte) uint32 {
	for i, b := range data {
		if i&1 == 0 {
			v = (v >> 4) ^ v ^ (v << 6) ^ uint32(b)
		} else {
			v = ^((v >> 7) ^ v ^ (uint32(b) | (v << 12)))
		}
	}
	return v
}
func signingBranch(q, body []byte, ts uint32) uint32 {
	low := signingIV(signingIV(signingIV(0x20230928, q), body), le32(ts)) & 15
	return low - ((low*171)>>9)*3
}
func signingChecksum(data []byte) uint32 {
	v := uint32(0x20220420)
	for i := 0; i < 12; i++ {
		if i&1 == 0 {
			v = uint32(data[i]) ^ (v << 7) ^ (v >> 3) ^ v
		} else {
			v = ^(uint32(data[i]) | (v << 11)) ^ (v >> 5) ^ v
		}
	}
	return (v | 4) ^ 0x1000000
}

func xGorgon(query string, body []byte, ts uint32, random uint16) string {
	qh, bh := md5.Sum([]byte(query)), signingBodyHash(body)
	data := join(qh[:4], bh[:4], make([]byte, 4), le32(67503104), be32(ts))
	key := []byte{0x4a, 0x40, 0x16, byte(random >> 8), 0x47, 0x6c, 1, byte(random)}
	var s [256]byte
	for i := range s {
		s[i] = byte(i)
	}
	j := 0
	for i := range s {
		j = (j + int(s[i]) + int(key[i%len(key)])) & 255
		s[i] = s[j]
	}
	j = 0
	for at, v := range data {
		i := at + 1
		x := s[i]
		j += int(x)
		y := s[j&255]
		s[i] = y
		data[at] = v ^ s[(int(y)*2)&255]
	}
	for i, v := range data {
		next := data[(i+1)%len(data)]
		data[i] = ^(bits.Reverse8(next^bits.RotateLeft8(v, 4)) ^ 20)
	}
	return hex.EncodeToString(join([]byte{0x84, 4, byte(random), byte(random >> 8), 0x40, 1}, data))
}

func helios(ts, random uint32) string {
	digest := md5.Sum(join(le32(random), []byte("8662")))
	ascii := []byte(hex.EncodeToString(digest[:]))
	words := []uint64{}
	for i := 0; i < 4; i++ {
		words = append(words, binary.LittleEndian.Uint64(ascii[i*8:]))
	}
	b0, b8 := words[0], words[1]
	table := []uint64{b0}
	words = words[2:]
	for i := 0; i < 34; i++ {
		x := (bits.RotateLeft64(b8, -8) + b0) ^ uint64(i)
		words = append(words, x)
		x ^= bits.RotateLeft64(b0, -61)
		table = append(table, x)
		b0, b8 = x, words[0]
		words = words[1:]
	}
	plain := pkcs7([]byte(fmt.Sprintf("%d-1588093228-8662", ts)))
	out := le32(random)
	for at := 0; at < len(plain); at += 16 {
		a, b := binary.LittleEndian.Uint64(plain[at:]), binary.LittleEndian.Uint64(plain[at+8:])
		for i := 0; i < 34; i++ {
			b = table[i] ^ (a + bits.RotateLeft64(b, -8))
			a = b ^ bits.RotateLeft64(a, -61)
		}
		out = binary.LittleEndian.AppendUint64(out, a)
		out = binary.LittleEndian.AppendUint64(out, b)
	}
	return base64.StdEncoding.EncodeToString(out)
}

// Minimal protobuf encoding for the signing SDK's envelope (sint uses zigzag).
type signField struct {
	tag   int
	value any
}

func sf(tag int, value any) signField { return signField{tag, value} }
func proto(fields ...signField) []byte {
	var out []byte
	for _, f := range fields {
		switch v := f.value.(type) {
		case int:
			out = binary.AppendUvarint(out, uint64(f.tag<<3))
			out = binary.AppendUvarint(out, uint64(int64(v)<<1)^uint64(int64(v)>>63))
		case int64:
			out = binary.AppendUvarint(out, uint64(f.tag<<3))
			out = binary.AppendUvarint(out, uint64(v<<1)^uint64(v>>63))
		case uint32:
			out = binary.AppendUvarint(out, uint64(f.tag<<3))
			out = binary.AppendUvarint(out, uint64(v)*2)
		case float32:
			out = binary.AppendUvarint(out, uint64(f.tag<<3|5))
			out = append(out, 0, 0, 0, 0) // SDK's zero-valued sensors
		case string:
			if v != "" {
				out = appendProtoBytes(out, f.tag, []byte(v))
			}
		case []byte:
			out = appendProtoBytes(out, f.tag, v)
		default:
			panic("unsupported signing field")
		}
	}
	return out
}
func appendProtoBytes(out []byte, tag int, b []byte) []byte {
	out = binary.AppendUvarint(out, uint64(tag<<3|2))
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}
func deviceProto(id, nonce string, started int64) []byte {
	return proto(sf(1, 1), sf(2, 2), sf(3, "8662"), sf(4, id), sf(5, nonce), sf(6, "!noperm!"), sf(7, -888888), sf(8, -888888), sf(9, 3), sf(10, -888888), sf(11, "!notset!"), sf(12, "Asia/Shanghai,8"), sf(13, "zh_CN"), sf(14, 4), sf(16, float32(0)), sf(17, float32(0)), sf(18, float32(0)), sf(19, float32(0)), sf(20, float32(0)), sf(21, float32(0)), sf(22, "16"), sf(23, 41), sf(24, 36), sf(25, started), sf(26, started), sf(27, started), sf(28, started), sf(29, -1), sf(30, "25053RT47C"), sf(31, "Redmi"), sf(32, "25053RT47C"), sf(33, "25053RT47C"), sf(34, "Xiaomi"), sf(35, "Redmi"), sf(36, "Redmi"), sf(38, 31))
}

func buildMedusa(query string, body []byte, ts uint32, dev *device, nonce string, started int64, nextRandom func() uint32, versionName string) string {
	bh := signingBodyHash(body)
	qh := sm3([]byte(query))
	f13 := hashF13(qh, bh[:], ts)
	nested := proto(sf(1, 111), sf(2, 10), sf(3, 694367), sf(5, 586952199))
	messageRand := nextRandom()
	envLaunch := int(uint64(nextRandom())*21>>32) + 100
	envPID := int(uint64(nextRandom())*2000>>32) + 10001
	env := proto(sf(1, envLaunch), sf(2, 146331399), sf(3, 146331396), sf(5, 7), sf(6, "v04.06.04.03-bugfix"), sf(7, envPID), sf(12, deviceProto(dev.DeviceID, nonce, started)), sf(13, proto(sf(1, ts), sf(2, -2), sf(4, 200))), sf(14, versionName))
	queryHash := sm3(join([]byte(query), bh[:], []byte("none")))
	message := proto(sf(1, unhex("f7e85ffad7d7dc3bd62ac87057cf6118")), sf(2, 3), sf(3, messageRand), sf(4, "8662"), sf(5, dev.DeviceID), sf(6, "1588093228"), sf(7, versionName), sf(8, "v04.06.04-ml-android"), sf(9, 67503104), sf(10, unhex("4001000000000000")), sf(12, ts), sf(13, f13), sf(14, qh[:6]), sf(15, nested), sf(16, nonce), sf(17, ts), sf(19, queryHash), sf(20, "none"), sf(21, 312), sf(23, env), sf(24, `{"cmr":16777216,"cmr2":16777216,"un_h":1879194040,"vpn":0,"kd":0,"fkd":3672518972,"pd":-1872573247,"dyn":"","do":0,"tk":true}`))
	random := nextRandom()
	signKey := unhex("8ebdfa3806ecc5cee79423e6029ed82540bc2218bb7eaef71cb691f7aa8aa2f5")
	hash := sm3(join(signKey, le32(random), signKey))
	d1 := (random >> 16) & 255
	seed := le32(^((d1<<11 | random>>24) ^ (d1 >> 5) ^ d1))
	transformed := join(unhex("4001000000000000"), xmxor(message, hash))
	for i, j := 0, len(transformed)-1; i < j; i, j = i+1, j-1 {
		transformed[i], transformed[j] = transformed[j], transformed[i]
	}
	for i := range transformed {
		transformed[i] ^= seed[(^i)&3]
	}
	check := uint32(qh[0]&63)<<14 | 0x18000001 | uint32(f13[0]&63)<<8
	packed := join([]byte{0x35}, le32(nextRandom()), le32(check), transformed, []byte{byte(random >> 16), byte(random >> 24)})
	encrypted := newAesV3(unhex("f1593376766ea98d34f31b057a9d5be4"), ts).encrypt(packed, unhex("1fe109a4125283f418de9e051a969e12"))
	version := unhex("03000000f7e85ffad7d7dc3bd62ac87057cf6118")
	var prefix []byte
	for i := 0; i < 20; i += 4 {
		prefix = append(prefix, le32(binary.LittleEndian.Uint32(version[i:])^ts)...)
	}
	return base64.StdEncoding.EncodeToString(join(prefix, []byte{byte(random), byte(random >> 8), 0, 1}, encrypted))
}

func xmxor(data, key []byte) []byte {
	out := make([]byte, len(data))
	for i, b := range data {
		at := (i * 4) & 28
		d0, d1 := key[at], key[at+1]
		v := bits.RotateLeft8(b, 4) + d0
		v = (^v) ^ d1
		v = bits.RotateLeft8(v, 3)
		v += d1
		v ^= d0
		out[len(data)-i-1] = ^v
	}
	n := len(out)
	last := out[n-1] ^ out[n-2]
	first := out[0]
	out[0] = ^last + first
	out[1] = (out[0] ^ out[n-1] ^ 254) + out[1]
	out[2] += ((last - first) ^ bits.RotateLeft8(out[1], 3) ^ 2)
	for i := 0; i < n-4; i++ {
		temp := bits.RotateLeft8(out[i+2], 3) ^ out[i+1] ^ byte(i+3)
		out[i+3] = ^temp + out[i+3]
	}
	out[n-1] ^= out[n-2]
	var sum byte
	for _, b := range out[1:] {
		sum += b
	}
	out[0] = (out[0] ^ out[1]) + sum
	return out
}

// SM3 (GB/T 32905), used by Medusa's query and envelope hashes.
func sm3(data []byte) []byte {
	size := uint64(len(data)) * 8
	padded := append([]byte(nil), data...)
	padded = append(padded, 0x80)
	for len(padded)%64 != 56 {
		padded = append(padded, 0)
	}
	padded = binary.BigEndian.AppendUint64(padded, size)
	h := [8]uint32{0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600, 0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e}
	for at := 0; at < len(padded); at += 64 {
		var w [68]uint32
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(padded[at+i*4:])
		}
		for i := 16; i < 68; i++ {
			x := w[i-16] ^ w[i-9] ^ bits.RotateLeft32(w[i-3], 15)
			w[i] = x ^ bits.RotateLeft32(x, 15) ^ bits.RotateLeft32(x, 23) ^ bits.RotateLeft32(w[i-13], 7) ^ w[i-6]
		}
		a, b, c, d, e, f, g, hh := h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7]
		for i := 0; i < 64; i++ {
			t := uint32(0x79cc4519)
			ff, gg := a^b^c, e^f^g
			if i >= 16 {
				t = 0x7a879d8a
				ff = (a & b) | (a & c) | (b & c)
				gg = (e & f) | (^e & g)
			}
			ar := bits.RotateLeft32(a, 12)
			s1 := bits.RotateLeft32(ar+e+bits.RotateLeft32(t, i), 7)
			s2 := s1 ^ ar
			tt1 := ff + d + s2 + (w[i] ^ w[i+4])
			tt2 := gg + hh + s1 + w[i]
			d, c, b, a = c, bits.RotateLeft32(b, 9), a, tt1
			hh, g, f, e = g, bits.RotateLeft32(f, 19), e, tt2^bits.RotateLeft32(tt2, 9)^bits.RotateLeft32(tt2, 17)
		}
		for i, v := range [8]uint32{a, b, c, d, e, f, g, hh} {
			h[i] ^= v
		}
	}
	var out []byte
	for _, v := range h {
		out = binary.BigEndian.AppendUint32(out, v)
	}
	return out
}

// Only branch 0 is emitted: signedRequest searches _rticket before signing.
func hashF13(q, body []byte, ts uint32) []byte {
	iv := signingIV(signingIV(signingIV(0x20230928, q), body), le32(ts))
	index := ((iv & 15) * 171) >> 9
	iv1 := []uint32{0xc4a78580, 0xb3c0fd39, 0xc58c5686, 0xc9aa3ba7, 0xf5a7adf2, 0x963c2ed1}[index]
	count2 := iv1 + ts
	var tt [64]uint32
	for i, v := range f13Table {
		tt[i] = bits.RotateLeft32(v, -int((count2+1)&31))
	}
	pad := make([]byte, 4)
	for i, v := range []uint32{0xfa, 0x45, 0x61, 0xd7} {
		pad[i] = byte((v | v<<8) >> ((count2 + 2) & 7))
	}
	data := join(q, body, le32(ts), pad, unhex("00000000000001a0"))
	var di [128]uint32
	for i := 0; i < 16; i++ {
		di[i] = binary.BigEndian.Uint32(data[i*4:])
	}
	for i := 0; i < 112; i++ {
		d1, d14 := di[i+1], di[i+14]
		r1 := bits.RotateLeft32(d1, 14) ^ bits.RotateLeft32(d1, 25) ^ (d1 >> 3)
		r2 := bits.RotateLeft32(d14, 13) ^ bits.RotateLeft32(d14, 15) ^ (d14 >> 10)
		di[i+16] = di[i] + di[i+9] + r1 + r2
	}
	initial := [8]uint32{0x7aba4fc8, 0x67166507, 0x6403fa00, 0x340f512f, 984304912, 3005047866, 2874125293, 2152413264}
	for i := range initial {
		initial[i] = bits.RotateLeft32(initial[i], -int(count2&31))
	}
	chosen := [6][11]int{{101, 5, 7, 6, 3, 2, 1, 0, 5, 4, 3}, {96, 0, 6, 7, 5, 3, 2, 1, 5, 4, 4}, {96, 7, 6, 2, 1, 4, 0, 5, 4, 3, 5}, {99, 3, 6, 2, 4, 5, 1, 0, 0, 7, 6}, {96, 0, 5, 6, 7, 3, 1, 2, 5, 4, 4}, {100, 2, 0, 3, 5, 4, 6, 7, 2, 1, 5}}[index]
	d := initial
	for i := 0; i < chosen[0]; i++ {
		base := iv1 + uint32(i)
		n1 := ((d[chosen[3]] ^ d[chosen[4]]) & d[chosen[1]]) ^ d[chosen[3]]
		n2 := bits.RotateLeft32(d[chosen[1]], 26) ^ bits.RotateLeft32(d[chosen[1]], 21) ^ bits.RotateLeft32(d[chosen[1]], 7)
		n4 := di[base&127] + n1 + n2 + tt[base&63] + d[chosen[5]]
		n5 := bits.RotateLeft32(d[chosen[2]], 30) ^ bits.RotateLeft32(d[chosen[2]], 19) ^ bits.RotateLeft32(d[chosen[2]], 10)
		n6 := (d[chosen[2]] & d[chosen[6]]) | ((d[chosen[2]] | d[chosen[6]]) & d[chosen[7]])
		old := d[chosen[9]]
		d = [8]uint32{d[7], d[0], d[1], d[2], d[3], d[4], d[5], d[6]}
		d[chosen[10]] = n5 + n6 + n4
		d[chosen[8]] = old + n4
	}
	var out []byte
	for i := range d {
		out = append(out, be32(d[i]+initial[i])...)
	}
	for i := 0; i < 16; i++ {
		out[i] ^= out[i+16]
	}
	return join(out[:16], le32(signingChecksum(out[:16])))
}

type aesV3 struct {
	box         []byte
	permutation [4]int
	order       [16]int
	keys        [48]byte
}

func newAesV3(key []byte, ts uint32) *aesV3 {
	n := ts & 3
	a := &aesV3{
		box:         medusaSbox[n*256 : (n+1)*256],
		permutation: [4][4]int{{1, 0, 2, 3}, {2, 0, 3, 1}, {0, 1, 3, 2}, {1, 0, 2, 3}}[n],
		order:       [4][16]int{{0, 9, 14, 11, 4, 13, 2, 7, 8, 1, 6, 15, 12, 5, 10, 3}, {0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11}, {0, 9, 14, 7, 4, 13, 2, 11, 8, 1, 6, 3, 12, 5, 10, 15}, {0, 9, 14, 11, 4, 13, 2, 7, 8, 1, 6, 15, 12, 5, 10, 3}}[n],
	}
	initial := []uint32{0xca025ddc, 0x823dc546, 0xc9420583, 0xc298225f}[n]
	for i := 0; i < 16; i++ {
		a.keys[i] = le32(initial)[i%4] ^ key[i]
	}
	rounds := 8
	for i := 4; i < 12; i++ {
		at := 4 * (i - 1)
		x, b, c, d := a.keys[at], a.keys[at+1], a.keys[at+2], a.keys[at+3]
		if i&3 == 0 {
			x, b, c, d = byte(initial>>uint(rounds&24))^a.box[b], a.box[c], a.box[d], a.box[x]
		}
		rounds += 2
		a.keys[at+4] = x ^ a.keys[at-12]
		a.keys[at+5] = b ^ a.keys[at-11]
		a.keys[at+6] = c ^ a.keys[at-10]
		a.keys[at+7] = d ^ a.keys[at-9]
	}
	return a
}
func (a *aesV3) block(value []byte) []byte {
	var rows [4][4]byte
	for i := range rows {
		copy(rows[i][:], value[i*4:])
	}
	addCon := func(round int) {
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				rows[i][j] ^= a.keys[round*16+i*4+a.permutation[j]]
			}
		}
	}
	addCon(0)
	for round := 1; round < 3; round++ {
		old := rows
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				rows[i][j] = a.box[old[a.permutation[i]][j]]
			}
		}
		old = rows
		for i, at := range a.order {
			rows[i/4][i%4] = old[at/4][at%4]
		}
		if round == 1 {
			old = rows
			for i := 0; i < 4; i++ {
				for j := 0; j < 4; j++ {
					rows[i][j] = old[i][a.permutation[j]]
				}
			}
			xt := func(x byte) byte {
				if x&128 != 0 {
					return x<<1 ^ 0x1b
				}
				return x << 1
			}
			for i := 0; i < 4; i++ {
				t := rows[0][i] ^ rows[1][i] ^ rows[2][i] ^ rows[3][i]
				u := rows[0][i]
				rows[0][i] ^= t ^ xt(rows[0][i]^rows[1][i])
				rows[1][i] ^= t ^ xt(rows[1][i]^rows[2][i])
				rows[2][i] ^= t ^ xt(rows[2][i]^rows[3][i])
				rows[3][i] ^= t ^ xt(rows[3][i]^u)
			}
		}
		addCon(round)
	}
	var out []byte
	for i := range rows {
		for j := range rows[i] {
			out = append(out, rows[i][j]^a.keys[16+i*4+j])
		}
	}
	return out
}
func (a *aesV3) encrypt(source, iv []byte) []byte {
	var plain [32]byte
	for i := 0; i < 31; i++ {
		at := i * 8
		plain[i] = (source[at]>>4)&2 | source[at+1]&64 | (source[at+2]>>2)&1 | (source[at+3]<<3)&128 | (source[at+4]>>1)&4 | (source[at+5]<<3)&16 | (source[at+6]<<5)&32 | (source[at+7]>>4)&8
	}
	plain[31] = 1
	var key []byte
	previous := iv
	for i := 0; i < 32; i += 16 {
		block := append([]byte(nil), plain[i:i+16]...)
		for j := range block {
			block[j] ^= previous[j]
		}
		previous = a.block(block)
		key = append(key, previous...)
	}
	out := append([]byte(nil), source...)
	for i := 0; i < 31; i++ {
		at := i * 8
		k := key[i]
		out[at] = out[at]&0xdf | (k<<4)&32
		out[at+1] = out[at+1]&0xbf | k&64
		out[at+2] = out[at+2]&0xfb | (k<<2)&4
		out[at+3] = out[at+3]&0xef | (k>>3)&16
		out[at+4] = out[at+4]&0xf7 | (k+k)&8
		out[at+5] = out[at+5]&0xfd | (k>>3)&2
		out[at+6] = out[at+6]&0xfe | (k>>5)&1
		out[at+7] = out[at+7]&0x7f | (k<<4)&128
	}
	return append([]byte{key[len(key)-1]}, out...)
}

var f13Table = [...]uint32{0xebb64faf, 0x7aadcc2, 0xcf3187bf, 0xe01138ff, 0x6d0bfcff, 0x5a30a3be, 0xb41ad638, 0x34180eb8, 0xf233eb6f, 0xb1a584cc, 0xccc30dc7, 0x47d1db51, 0xd55653de, 0x70a84fa1, 0x57473c12, 0xf76f0288, 0x2c077f0a, 0xda0dcad0, 0xfbb86f6c, 0xfdc4cf00, 0x688a020d, 0xe676c6a6, 0x8cd6338b, 0x1a3c8d0e, 0xcce8b06b, 0x6ad0ed0b, 0xa0522717, 0xdc71ac83, 0x2285db71, 0xd5b4dda6, 0x736f8650, 0x6560306c, 0x617ce2a6, 0xe423417e, 0xa40e143, 0x544e4032, 0x88dffb2a, 0x716c1ae0, 0x4c467a88, 0x5b23bb3, 0xe1d0b866, 0xbaa3dcb8, 0xae3374d3, 0xc3381a50, 0x1702f75b, 0xfe6da368, 0xf0b4cf48, 0x4e0ffbb8, 0x72aad10d, 0x26c53a3d, 0xf2bce0f6, 0xb4557581, 0x4a257fdd, 0x8c3182a2, 0xab0b3b86, 0x3d5dfb14, 0x4f103634, 0xd37b52d7, 0x444eff16, 0xeb0a33d1, 0x6ca86f6e, 0x284ba7, 0x8387cfa, 0x5fb37586}

var medusaSbox, _ = base64.StdEncoding.DecodeString("+n0Ia5xZs0sEXznQOEqRmQBnpiCf9U2Ccybu3xhmgzOAAxn72f6uqqmwUsYL83klTni0NqxdGieeiNu9PGPsSRXBMB/cuFbUbM3KCUPINaPvHvSW0vwOcnuUhNHqRVpiAj/TEoE0K91+5ijypUYTATsh9mE3KSoN7Yyvv51cuyR2D3XkU4nhmI2xmmVwT1RMWKtub4sjxAcRDLrPoKSO2AU9FLLadMPX577Wf95IFj6FkKFVt3dCIsmGUC4X+WQxLJvxbRxEaOPpqJOXyzJX6+Vxaq3AzMfF/WAdoi1Hp+JRaV56zgpBtpWP97mH4DoGEIq1+FvV8LyS/3wvwugbQOwb2r26mJEMsiuDQTRn+wrYdrVGBVlhI3WQhyrjUBVMrLF5667llUcEaPCGPVGLD8qO5LlO8hKCvA7V9+8oJc9bXelqVQLhM76T5/WtnT45JKji+hdX0HoNCDDWuKON/QeaxB5uImSX0h2wv0VmP2zd2yeApxHcpsVS+MC2yFwAc2B7oBkTqsk1SEvTpM2fmfMQREBUfin0Bh+iq6EvPPavhWI2IX9e3yAas7Tm/3KEj2UmlFp36kN4x0rMLBRrxuh0U/zUHM4xcAMYjJY4MonxOl/X+alpt2M3WMI7w3HLnpIBigtNiJu7T21v4P6lSd5WFgntnMEt7oF92XzRLkJbTcGmXepE/UVOG6E/0YnhfS+q26utWcuxzpooyeD2cDlK1/8w9d28VzsRjbLuALbmGlp8+d7EzS6Au7lMpZ+ECMZvQmzwJ+eLOpxR+2chdUExp8ogQyq3v9l68rX4jCwjg0+PYKAEEzcU4wHFY2ZcdIHfWL1okD3SszT0GZMyKdZJrg1L2AeerB4tC0C4crp2EHGo5FYdSP7lwkeR2ocmnR+Ia8CYviUJlzOjhRZef9xuVOn3qcjow3fQgivsAmKKkg4+sA8F8/GWeDiGNhg8JM8KtFPMYWWkx5TVFX5t73kiNRJqjlIGVXtGZFCV4gzt0xcDopuZ6xz8r9Rzafpf9ywev8jh8592gHFIqpStZPuJxmDDMrNN0uBE3V+oscdoIzTJbRJ/t+sVvqnReJOgDJKk10fjisJwqyZBmnmn2BSFj8BvVtCMEbkuPOKdzw7eA11GPs04Qw8zWtkaZWwiO/wwpojqN6K0jY5RnNZA7vn4hPSul+nKCkVnVwQvg1zVxcSCtqORmB9KrJaBbssbCQivGJVJfVTt+hYxOtq4ZvWl8f4QAQZ0zGPffCgl9s6yT4vlvIdpu4YhBwA25wtQWZsc6GJYGWHyvSdeuh3mmUI9DSq13Fsp8C1MU3tqc04/df9LoTUXVXI5INOw/e8C7Hd+5CvbkMEFnnrUUmskEw==")

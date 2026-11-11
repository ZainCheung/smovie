// SPDX-License-Identifier: GPL-3.0-only
// Argus 551 and Ladon wire protocol ported from ch6vip/fqapp's GPL-3.0
// rust/src/sign/{argus,ladon,simon}.rs at 26372004f052fb81d6ce61a09de89fc7555b1d46.
// Cross-checked with the independent fixed vectors from
// zfdang/hongguo-open-player PR #92 (src/server/native/argus.js, GPL-3.0-only).
package hongguo

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"math/bits"
	"net/url"
	"strconv"
)

const playbackLicenseID = "1611921764"

// Argus uses unsigned protobuf varints; Medusa's zigzag writer cannot be used.
type argusProto []byte

func (p *argusProto) number(field int, value uint32) {
	*p = binary.AppendUvarint(*p, uint64(field<<3))
	*p = binary.AppendUvarint(*p, uint64(value))
}

func (p *argusProto) blob(field int, data []byte) {
	*p = appendProtoBytes(*p, field, data)
}

func playbackArgus(query string, body []byte, ts, nonce uint32) string {
	q, _ := url.ParseQuery(query)
	var bean, counters, device argusProto
	bean.number(1, 1077940818)
	bean.number(2, 2)
	bean.number(3, nonce)
	for _, f := range []struct {
		field int
		value string
	}{
		{4, firstNonEmpty(q.Get("aid"), "1967")}, {5, q.Get("device_id")},
		{6, playbackLicenseID}, {7, q.Get("version_name")}, {8, "v04.04.05-ov-android"},
	} {
		bean.blob(f.field, []byte(f.value))
	}
	bean.number(9, 134744640)
	bean.blob(10, make([]byte, 8))
	bean.number(11, 0)
	bean.number(12, ts<<1)
	bodyMD5 := [16]byte{}
	if len(body) != 0 {
		bodyMD5 = md5.Sum(body)
	}
	bean.blob(13, sm3(bodyMD5[:])[:6])
	queryBytes := []byte(query)
	if len(queryBytes) == 0 {
		queryBytes = make([]byte, 16)
	}
	bean.blob(14, sm3(queryBytes)[:6])
	for _, f := range [][2]uint32{{1, 1}, {2, 1}, {3, 1}, {7, 0xc792eccc}} {
		counters.number(int(f[0]), f[1])
	}
	bean.blob(15, counters)
	bean.blob(16, []byte(q.Get("iid")))
	bean.blob(20, []byte("none"))
	bean.number(21, 738)
	device.blob(1, []byte("NX551J"))
	device.number(2, 8196)
	device.number(4, 0x80e0d800)
	bean.blob(23, device)
	bean.number(25, 2)

	// Simon with 64-bit words and 72 rounds over the padded protobuf message.
	key := unhex("fc78e0a9657a0c748ce51559903ccf03510e51d3cff232d71343e88a321c5304")
	var roundKeys [72]uint64
	for i := 0; i < 4; i++ {
		roundKeys[i] = binary.LittleEndian.Uint64(key[i*8:])
	}
	const z uint64 = 0x3dc94c3a046d678b
	for i := 4; i < len(roundKeys); i++ {
		t := bits.RotateLeft64(roundKeys[i-1], -3) ^ roundKeys[i-3]
		roundKeys[i] = ^roundKeys[i-4] ^ t ^ bits.RotateLeft64(t, -1) ^ ((z >> ((i - 4) % 62)) & 1) ^ 3
	}
	message := pkcs7(bean)
	for offset := 0; offset < len(message); offset += 16 {
		x := binary.LittleEndian.Uint64(message[offset:])
		y := binary.LittleEndian.Uint64(message[offset+8:])
		for _, k := range roundKeys {
			x, y = y, x^(bits.RotateLeft64(y, 1)&bits.RotateLeft64(y, 8))^bits.RotateLeft64(y, 2)^k
		}
		binary.LittleEndian.PutUint64(message[offset:], x)
		binary.LittleEndian.PutUint64(message[offset+8:], y)
	}
	mask := unhex("f2f7fcfff2f7fcff")
	frame := join(mask, message, unhex("eafb2cfe8568519154"))
	for i := len(mask); i < len(frame); i++ {
		frame[i] ^= mask[i%len(mask)]
	}
	for i, j := 0, len(frame)-1; i < j; i, j = i+1, j-1 {
		frame[i], frame[j] = frame[j], frame[i]
	}
	frame = pkcs7(append(frame, 'a', 'o'))
	block, _ := aes.NewCipher(unhex("d31e3718288a1027baab59f146a09a9c"))
	cipher.NewCBCEncrypter(block, unhex("ea180a0336ed352fcd24e4d50018ae54")).CryptBlocks(frame, frame)
	return base64.StdEncoding.EncodeToString(join([]byte{0xf2, 0x81}, frame))
}

func playbackLadon(ts uint32, aid string, nonce uint32) string {
	salt := le32(nonce)
	digest := md5.Sum(join(salt, []byte(aid)))
	key := []byte(hex.EncodeToString(digest[:]))
	var schedule [35]uint64
	schedule[0] = binary.LittleEndian.Uint64(key)
	pending := []uint64{binary.LittleEndian.Uint64(key[8:]), binary.LittleEndian.Uint64(key[16:]), binary.LittleEndian.Uint64(key[24:])}
	for i := 0; i < 34; i++ {
		next := (bits.RotateLeft64(pending[i], -8) + schedule[i]) ^ uint64(i)
		pending = append(pending, next)
		schedule[i+1] = next ^ bits.RotateLeft64(schedule[i], 3)
	}
	message := pkcs7([]byte(strconv.FormatUint(uint64(ts), 10) + "-" + playbackLicenseID + "-" + aid))
	for offset := 0; offset < len(message); offset += 16 {
		a := binary.LittleEndian.Uint64(message[offset:])
		b := binary.LittleEndian.Uint64(message[offset+8:])
		for _, k := range schedule[:34] {
			b = (a + bits.RotateLeft64(b, -8)) ^ k
			a = bits.RotateLeft64(a, 3) ^ b
		}
		binary.LittleEndian.PutUint64(message[offset:], a)
		binary.LittleEndian.PutUint64(message[offset+8:], b)
	}
	return base64.StdEncoding.EncodeToString(join(salt, message))
}

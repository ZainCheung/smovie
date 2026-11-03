// SPDX-License-Identifier: GPL-3.0-only
// Comment (danmaku) signing ported from jackotom/hongguo-drama
// src/native/danmaku-sign.js, itself adapted from dawei233/hongguo-drama
// (commit c884fab6c6e36ad38173e251738b9b35f958383f) and nas-backend's
// danmaku.py. The comment API takes its own X-Gorgon, X-Ladon and X-Argus,
// unlike playback's. testdata/danmaku-sign-vectors.json pins them byte for
// byte; what looks like a slip below (the key schedule assigning rather
// than swapping) is what the server accepts.
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

// commentNonceSize is the random bytes a signature takes: [0:2] go to the
// Gorgon, [2:6] to the Ladon and [6:11] to the Argus.
const commentNonceSize = 11

func commentQuery(stampMs int64, deviceID, installID string) string {
	q := url.Values{}
	for k, v := range map[string]string{
		"aid": "8662", "app_name": "novelread", "version_code": "73532", "version_name": "7.3.5.32",
		"manifest_version_code": "73532", "update_version_code": "73532", "channel": "update_64",
		"device_platform": "android", "os": "android", "ssmix": "a", "device_type": "25053RT47C",
		"device_brand": "Redmi", "language": "zh", "os_api": "36", "os_version": "16",
		"resolution": "1280*2772", "dpi": "520", "ac": "wifi",
	} {
		q.Set(k, v)
	}
	q.Set("device_id", deviceID)
	q.Set("iid", installID)
	q.Set("_rticket", strconv.FormatInt(stampMs, 10))
	return q.Encode() // sorted, as Python's urlencode of the sorted pairs
}

func commentHeaders(rawQuery string, stamp int64, nonce []byte) map[string]string {
	return map[string]string{
		"X-Gorgon":        commentGorgon(rawQuery, stamp, nonce),
		"X-Khronos":       strconv.FormatInt(stamp, 10),
		"X-SS-Req-Ticket": strconv.FormatInt(stamp*1000, 10),
		"X-SS-STUB":       "",
		"X-Argus":         commentArgus(rawQuery, stamp, nonce),
		"X-Ladon":         commentLadon("8662", stamp, nonce[2:6]),
	}
}

func rotl8(x byte, n int) byte { return bits.RotateLeft8(x, n&7) }

func commentGorgon(rawQuery string, stamp int64, nonce []byte) string {
	head2 := byte(((int(nonce[0]) % 7) + 1) << 5)
	head3 := 0xE0 | nonce[1]&0x0F
	seed := make([]byte, 20)
	digest := md5.Sum([]byte(rawQuery))
	copy(seed, digest[:4])
	copy(seed[12:], []byte{0, 1, 7, 4})
	binary.BigEndian.PutUint32(seed[16:], uint32(stamp))
	key := []byte{0x4A, 0x00, 0x16, head3, 0x47, 0x6C, 0x00, head2}

	var box [256]byte
	for i := range box {
		box[i] = byte(i)
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j + int(box[i]) + int(key[i&7])) & 0xFF
		box[i] = box[j] // an assignment, not a swap
	}
	j = 0
	for off := 0; off < 20; off++ {
		i := (off + 1) & 0xFF
		j = (j + int(box[i])) & 0xFF
		box[i] = box[j]
		seed[off] ^= box[(int(box[i])+int(box[j]))&0xFF]
	}
	for i := 0; i < 20; i++ {
		seed[i] = bits.Reverse8(rotl8(seed[i], 4)^seed[(i+1)%20]) ^ 0xEB
	}
	return hex.EncodeToString(append([]byte{0x84, 0x04, head2, head3, 0, 0}, seed...))
}

func le64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }

func commentLadon(aid string, stamp int64, prefix []byte) string {
	sum := md5.Sum(append(append([]byte(nil), prefix...), aid...))
	digest := []byte(hex.EncodeToString(sum[:]))
	left, right := le64(digest[0:8]), le64(digest[8:16])
	queue := []uint64{le64(digest[16:24]), le64(digest[24:32])}
	keys := make([]uint64, 0, 34)
	for i := 0; i < 34; i++ {
		keys = append(keys, left)
		mixed := (bits.RotateLeft64(right, -8) + left) ^ uint64(i)
		queue = append(queue, mixed)
		left = mixed ^ bits.RotateLeft64(left, 3)
		right, queue = queue[0], queue[1:]
	}
	data := pkcs7([]byte(strconv.FormatInt(stamp, 10) + "-1611921764-3019"))
	for off := 0; off < len(data); off += 16 {
		bl, br := le64(data[off:]), le64(data[off+8:])
		for _, k := range keys {
			br = k ^ (bl + bits.RotateLeft64(br, -8))
			bl = br ^ bits.RotateLeft64(bl, 3)
		}
		binary.LittleEndian.PutUint64(data[off:], bl)
		binary.LittleEndian.PutUint64(data[off+8:], br)
	}
	return base64.StdEncoding.EncodeToString(append(append([]byte(nil), prefix...), data...))
}

var argusKey = unhex("ac1adaae95a7af94a5114ab3b3a97dd80050aa0a39314c40528caec95256c28c")

func commentArgus(rawQuery string, stamp int64, nonce []byte) string {
	var out []byte
	varint := func(field int, v uint64) {
		out = binary.AppendUvarint(out, uint64(field)*8)
		out = binary.AppendUvarint(out, v)
	}
	field := func(field int, b []byte) {
		out = binary.AppendUvarint(out, uint64(field)*8+2)
		out = binary.AppendUvarint(out, uint64(len(b)))
		out = append(out, b...)
	}
	varint(1, 0x20200929*2)
	varint(2, 2)
	varint(3, uint64(binary.LittleEndian.Uint32(nonce[6:10])))
	field(4, []byte("3019"))
	field(6, []byte("1611921764"))
	field(7, []byte("6.8.1.32"))
	field(8, []byte("v04.07.01-ml-android"))
	varint(9, 135135744)
	field(10, make([]byte, 8))
	varint(12, uint64(stamp)*2)
	field(13, sm3(make([]byte, 16))[:6])
	field(14, sm3([]byte(rawQuery))[:6])
	nested := []byte{0x08, byte(2 + (int(nonce[10])%49)*2)}
	for _, f := range []uint64{2, 3, 4} {
		nested = binary.AppendUvarint(nested, f*8)
		nested = binary.AppendUvarint(nested, 1388734)
	}
	field(15, nested)
	field(20, []byte("none"))
	varint(21, 738)
	data := pkcs7(out)

	derived := sm3(join(argusKey, []byte{0x3C, 0xCC, 0x56, 0x7B}, argusKey))
	var rounds [72]uint64
	for i := 0; i < 4; i++ {
		rounds[i] = le64(derived[i*8:])
	}
	for i := 4; i < 72; i++ {
		mixed := bits.RotateLeft64(rounds[i-1], -3) ^ rounds[i-3]
		mixed ^= bits.RotateLeft64(mixed, -1)
		rounds[i] = ^rounds[i-4] ^ mixed ^ (uint64(0x3DC94C3A046D678B)>>uint((i-4)%62))&1 ^ 3
	}
	for off := 0; off < len(data); off += 16 {
		left, right := le64(data[off:]), le64(data[off+8:])
		for _, r := range rounds {
			next := left ^ (bits.RotateLeft64(right, 1) & bits.RotateLeft64(right, 8)) ^ bits.RotateLeft64(right, 2) ^ r
			left, right = right, next
		}
		binary.LittleEndian.PutUint64(data[off:], left)
		binary.LittleEndian.PutUint64(data[off+8:], right)
	}
	xor := []byte{0xD0, 0x4F, 0xFD, 0xFF, 0xD0, 0x4F, 0xFD, 0xFF}
	for i := range data {
		data[i] ^= xor[i%8]
	}
	reversed := join(xor, data)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	container := pkcs7(join([]byte{0xA6, 0xE7, 0x83, 0xEE, 0x70, 0x01, 0x10, 0x09, 0x18}, reversed, []byte{0x56, 0x7B}))
	k, iv := md5.Sum(argusKey[:16]), md5.Sum(argusKey[16:])
	block, _ := aes.NewCipher(k[:])
	enc := make([]byte, len(container))
	cipher.NewCBCEncrypter(block, iv[:]).CryptBlocks(enc, container) // already whole blocks: no more padding
	return base64.StdEncoding.EncodeToString(append([]byte{0x3C, 0xCC}, enc...))
}

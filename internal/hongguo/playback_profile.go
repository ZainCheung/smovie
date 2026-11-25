// SPDX-License-Identifier: GPL-3.0-only
// Full V5 playback wire context follows hongguo-drama b20413b,
// scripts/sdk-playback/vendor/phone_account/phone_sdk_bootstrap_v5.py.
// The existing Go branch-12 digest and carrier match the pinned official SDK.
// Requests bind directly to our anonymous registration; no SDK binary or
// session token is loaded.
package hongguo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/rc4"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func fullMobileParams(dev *device) url.Values {
	prof := activeProfile()
	pb := prof.raw.Playback
	q := url.Values{}
	for k, v := range prof.baseDeviceQuery() {
		q.Set(k, v)
	}
	for k, v := range map[string]string{
		"device_id": dev.DeviceID, "iid": dev.IID, "cdid": dev.CDID,
		"version_code": pb.FullVersionCode, "version_name": pb.FullVersionName, "manifest_version_code": pb.FullVersionCode,
		"update_version_code": pb.FullVersionCode, "rom_version": "W528JS release-keys",
		"device_type": pb.Register.DeviceModel, "device_brand": pb.Register.DeviceBrand,
	} {
		q.Set(k, v)
	}
	return q
}

func fullMobileDeviceProof(dev *device) []byte {
	reg := activeProfile().raw.Playback.Register
	proof := proto(sf(1, 1), sf(2, 2), sf(3, "8662"), sf(4, dev.DeviceID), sf(5, dev.CDID), sf(6, "!noperm!"),
		sf(7, -888888), sf(8, -888888), sf(9, 3), sf(10, -888888), sf(11, "!notset!"), sf(12, "Asia/Shanghai,8"), sf(13, "zh_CN"), sf(14, 4))
	// Fixed SDK sensor defaults, not readings from the user's computer.
	for i, v := range []float32{255.24993896484375, 35.58599090576172, 3.467449188232422, 3.467449188232422, 255.1754913330078, 42.17544174194336} {
		proof = binary.AppendUvarint(proof, uint64((16+i)<<3|5))
		proof = binary.LittleEndian.AppendUint32(proof, math.Float32bits(v))
	}
	return join(proof, proto(sf(22, "16"), sf(23, 41), sf(24, 36), sf(25, signingStarted), sf(26, signingStarted),
		sf(27, signingStarted), sf(28, signingStarted+2), sf(29, -1), sf(30, reg.DeviceModel), sf(31, reg.DeviceBrand),
		sf(32, reg.DeviceModel), sf(33, reg.DeviceModel), sf(34, reg.DeviceBrand), sf(35, reg.DeviceBrand), sf(36, reg.DeviceBrand), sf(38, 31)))
}

func signedFullMobileRequest(ctx context.Context, payload []byte, dev *device, now time.Time) (*http.Request, error) {
	prof := activeProfile()
	pb := prof.raw.Playback
	m5 := pb.MedusaV5
	q := fullMobileParams(dev)
	var model map[string]any
	if err := json.Unmarshal(payload, &model); err != nil {
		return nil, errors.New("播放请求无效")
	}
	biz, ok := model["biz_param"].(map[string]any)
	if !ok {
		return nil, errors.New("播放请求无效")
	}
	biz["device_level"] = pb.FullDeviceLevel
	payload, _ = json.Marshal(model)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write(payload)
	_ = zw.Close()
	body := compressed.Bytes()
	ts := uint32(now.Unix())
	bh := md5.Sum(body)
	var query string
	var qh []byte
	for n := 0; n < 1024; n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q.Set("_rticket", strconv.FormatInt(now.UnixMilli()+int64(n), 10))
		query = strings.ReplaceAll(q.Encode(), "+", "%20")
		qh = sm3([]byte(query))
		if signingIV(signingIV(signingIV(0x20230928, qh), bh[:]), le32(ts))&15 == 12 {
			break
		}
		if n == 1023 {
			return nil, errors.New("无法生成 SDK 请求摘要")
		}
	}
	flags := binary.LittleEndian.AppendUint64(nil, 0x0504)
	proof := hashF13Profile(qh, bh[:], ts, true)
	env := proto(sf(1, 1), sf(6, m5.EnvVersion), sf(7, os.Getpid()), sf(8, q.Get("channel")),
		sf(12, fullMobileDeviceProof(dev)), sf(14, q.Get("version_name")), sf(15, 2114), sf(19, now.UnixMilli()))
	state := m5.FullStateJSON
	message := proto(sf(1, prof.magic), sf(2, 5), sf(3, random32()), sf(4, "8662"), sf(5, dev.DeviceID), sf(6, m5.License),
		sf(7, q.Get("version_name")), sf(8, m5.SDKVersion), sf(9, m5.SDKCode), sf(10, flags), sf(12, ts), sf(13, proof), sf(14, qh[:6]),
		sf(15, proto(sf(1, 1), sf(2, 694367), sf(3, 694367), sf(5, 694367))), sf(17, ts), sf(20, "none"), sf(21, m5.FullField21Status), sf(23, env), sf(24, state))
	random := random32()
	hash := sm3(join(prof.signKey, le32(random), prof.signKey))
	high := random >> 16 & 255
	seed := le32(^((high<<11 | random>>24) ^ (high >> 5) ^ high))
	transformed := join(flags, xmxor(message, hash))
	for i, j := 0, len(transformed)-1; i < j; i, j = i+1, j-1 {
		transformed[i], transformed[j] = transformed[j], transformed[i]
	}
	for i := range transformed {
		transformed[i] ^= seed[(^i)&3]
	}
	check := uint32(0x18000001) | uint32(qh[0]&63)<<14 | uint32(proof[0]&63)<<8
	packed := join([]byte{0xca}, le32(random32()), le32(check), transformed, []byte{byte(random >> 16), byte(random >> 24)})
	key, iv := md5.Sum(prof.signKey[:16]), md5.Sum(prof.signKey[16:])
	encrypted := newSigningCarrier(key[:], ts, true).encrypt(packed, iv[:])
	header := join(le32(5), prof.magic)
	stamp := le32(ts)
	for i := range header {
		header[i] ^= stamp[i&3]
	}
	medusa := base64.StdEncoding.EncodeToString(join(header, []byte{byte(random), byte(random >> 8), 0, 1}, encrypted))
	var checksum uint64
	for _, b := range []byte(m5.License) {
		checksum ^= uint64(b)
		for i := 0; i < 8; i++ {
			if checksum&1 != 0 {
				checksum = checksum>>1 ^ 0x95ac9329ac4bc9b5
			} else {
				checksum >>= 1
			}
		}
	}
	allocation := uint64(random32())<<32 | uint64(random32())
	next := uint32(bits.RotateLeft64((allocation&^15)^checksum^uint64(ts), 4))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pb.AppAPIBase+"/novel/player/multi_video_model/player/v1?"+query, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("SDK 请求格式无效")
	}
	for k, v := range map[string]string{
		"Content-Type": "application/json; charset=utf-8", "Accept": "application/json", "Content-Encoding": "gzip", "x-tt-request-tag": "t=0;n=0", "x-ss-dp": "8662",
		"x-khronos": strconv.FormatUint(uint64(ts), 10), "x-argus": base64.StdEncoding.EncodeToString(le32(ts)),
		"x-ladon": base64.StdEncoding.EncodeToString(be32(next)), "x-gorgon": fullMobileGorgon(query, body, ts, uint16(random32())),
		"x-helios": heliosWithLicense(ts, random32(), m5.License), "x-medusa": medusa, "x-ss-stub": strings.ToUpper(hex.EncodeToString(bh[:])),
		"x-vc-bdturing-sdk-version": "4.0.3.cn", "x-tt-trace-id": "00-" + randomHex(16) + "-" + randomHex(8) + "-01",
		"User-Agent":        pb.FullMobileUA,
		"x-reading-request": fmt.Sprintf("%d-%d", now.UnixMilli(), 100000000+random32()%900000000),
		"x-ss-req-ticket":   strconv.FormatInt(now.UnixMilli(), 10),
	} {
		req.Header.Set(k, v)
	}
	cookies := map[string]string{"install_id": dev.IID, "store-region-src": "did"}
	cookieRequest := &http.Request{Header: http.Header{"Cookie": {dev.Cookie}}}
	for _, cookie := range cookieRequest.Cookies() {
		switch cookie.Name {
		case "odin_tt", "ttreq", "store-region":
			cookies[cookie.Name] = cookie.Value
		}
	}
	for name, value := range cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	req.Header.Set("x-tt-store-region", firstNonEmpty(cookies["store-region"], "cn"))
	req.Header.Set("x-tt-store-region-src", "did")
	if err := applyRemoteSigner(ctx, req, body); err != nil {
		return nil, err
	}
	return req, nil
}

func fullMobileGorgon(query string, body []byte, ts uint32, nonce uint16) string {
	flags := []byte{4, 5}
	qh, bh := md5.Sum([]byte(query)), md5.Sum(body)
	data := join(qh[:4], bh[:4], make([]byte, 4), unhex("00030a04"), be32(ts))
	key := []byte{0x4a, flags[0], 0x16, byte(nonce >> 8), 0x47, 0x6c, flags[1], byte(nonce)}
	stream, _ := rc4.NewCipher(key)
	stream.XORKeyStream(data, data)
	for i, v := range data {
		next := data[(i+1)%len(data)]
		data[i] = ^(bits.Reverse8(next^bits.RotateLeft8(v, 4)) ^ 20)
	}
	return hex.EncodeToString(join([]byte{0x84, 4, byte(nonce), byte(nonce >> 8), flags[0], flags[1]}, data))
}

func fullMobileJSON(req *http.Request) ([]byte, error) {
	client := *httpClient
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		var u *url.Error
		if errors.As(err, &u) {
			err = u.Err
		}
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return readJSONResponse(res)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if int64(len(body)) > 4<<20 {
		return nil, errors.New("播放响应过大")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errEmpty
	}
	return body, nil
}

// signedSingleVideoModelRequest builds a POST to /novel/player/video_model/v1/
// carrying both the 551 Argus/Ladon envelope and the real-device V5 Medusa
// header (field 21 = 369/312). Since 2026-11-23/25 the upstream service
// requires x-medusa alongside 551 headers on this endpoint, whereas dropping
// x-medusa or using the batch player/v1 endpoint returns an empty 200 body.
func signedSingleVideoModelRequest(ctx context.Context, vid string, dev *device, now time.Time) (*http.Request, error) {
	prof := activeProfile()
	pb := prof.raw.Playback
	m5 := pb.MedusaV5
	payload, err := json.Marshal(map[string]any{
		"video_id": vid,
		"biz_param": map[string]any{
			"video_id_type":             0,
			"need_all_video_definition": true,
			"video_platform":            3,
		},
	})
	if err != nil {
		return nil, errors.New("播放请求无效")
	}
	q := fullMobileParams(dev)
	ts := uint32(now.Unix())
	q.Set("ts", strconv.FormatUint(uint64(ts), 10))
	bh := md5.Sum(payload)
	var query string
	var qh []byte
	for n := 0; n < 1024; n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q.Set("_rticket", strconv.FormatInt(now.UnixMilli()+int64(n), 10))
		query = strings.ReplaceAll(q.Encode(), "+", "%20")
		qh = sm3([]byte(query))
		if signingIV(signingIV(signingIV(0x20230928, qh), bh[:]), le32(ts))&15 == 12 {
			break
		}
		if n == 1023 {
			return nil, errors.New("无法生成 SDK 请求摘要")
		}
	}
	flags := binary.LittleEndian.AppendUint64(nil, 0x0504)
	proof := hashF13Profile(qh, bh[:], ts, true)
	env := proto(sf(1, 1), sf(6, m5.EnvVersion), sf(7, os.Getpid()), sf(8, q.Get("channel")),
		sf(12, fullMobileDeviceProof(dev)), sf(14, q.Get("version_name")), sf(15, 2114), sf(19, now.UnixMilli()))
	message := proto(sf(1, prof.magic), sf(2, 5), sf(3, random32()), sf(4, "8662"), sf(5, dev.DeviceID), sf(6, m5.License),
		sf(7, q.Get("version_name")), sf(8, m5.SDKVersion), sf(9, m5.SDKCode), sf(10, flags), sf(12, ts), sf(13, proof), sf(14, qh[:6]),
		sf(15, proto(sf(1, 1), sf(2, 694367), sf(3, 694367), sf(5, 694367))), sf(17, ts), sf(20, "none"), sf(21, m5.FullField21Status), sf(23, env), sf(24, m5.FullStateJSON))
	random := random32()
	hash := sm3(join(prof.signKey, le32(random), prof.signKey))
	high := random >> 16 & 255
	seed := le32(^((high<<11 | random>>24) ^ (high >> 5) ^ high))
	transformed := join(flags, xmxor(message, hash))
	for i, j := 0, len(transformed)-1; i < j; i, j = i+1, j-1 {
		transformed[i], transformed[j] = transformed[j], transformed[i]
	}
	for i := range transformed {
		transformed[i] ^= seed[(^i)&3]
	}
	check := uint32(0x18000001) | uint32(qh[0]&63)<<14 | uint32(proof[0]&63)<<8
	packed := join([]byte{0xca}, le32(random32()), le32(check), transformed, []byte{byte(random >> 16), byte(random >> 24)})
	key, iv := md5.Sum(prof.signKey[:16]), md5.Sum(prof.signKey[16:])
	encrypted := newSigningCarrier(key[:], ts, true).encrypt(packed, iv[:])
	header := join(le32(5), prof.magic)
	stamp := le32(ts)
	for i := range header {
		header[i] ^= stamp[i&3]
	}
	medusa := base64.StdEncoding.EncodeToString(join(header, []byte{byte(random), byte(random >> 8), 0, 1}, encrypted))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pb.SingleVideoAPI+"?"+query, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("SDK 请求格式无效")
	}
	for k, v := range map[string]string{
		"Content-Type":       "application/json; charset=UTF-8",
		"Accept":             "application/json; charset=utf-8,application/x-protobuf",
		"User-Agent":         pb.PlaybackUA,
		"x-xs-from-web":      "0",
		"sdk-version":        "2",
		"passport-sdk-version": "50561",
		"x-khronos":          strconv.FormatUint(uint64(ts), 10),
		"x-ss-req-ticket":    q.Get("_rticket"),
		"x-ss-stub":          strings.ToUpper(hex.EncodeToString(bh[:])),
		"x-gorgon":           fullMobileGorgon(query, payload, ts, uint16(random32())),
		"x-helios":           heliosWithLicense(ts, random32(), m5.License),
		"x-medusa":           medusa,
		"x-argus":            playbackArgus(query, payload, ts, random32()),
		"x-ladon":            playbackLadon(ts, firstNonEmpty(q.Get("aid"), "8662"), random32()),
	} {
		req.Header.Set(k, v)
	}
	if err := applyRemoteSigner(ctx, req, payload); err != nil {
		return nil, err
	}
	return req, nil
}

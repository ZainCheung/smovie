// SPDX-License-Identifier: GPL-3.0-only
// Android v5 playback ported from N3urda/hongguoTV, commits 18c37c2 and
// 216fd3e. Its GPL signing implementation derives from drpys 22261ad.
// Constants describe the wire protocol; no captured user identity is included.
package hongguo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const mobileVideoAPI = "https://api5-normal-sinfonlinea.fqnovel.com/novel/player/multi_video_model/player/v1"
const mobileLicense = "646637794"

var mobileMagic = unhex("2d4b4fca49750d433fb5ae2c226dcc56")
var mobileSignKey = unhex("b23c2a470a20d105dc3f8c4438f8272ed46d6c89ffdcdedd5108698d26e9489d")

func mobileVideoPayload(vid string) []byte {
	body, _ := json.Marshal(map[string]any{
		"biz_param": map[string]any{"detail_page_version": 0, "device_level": 1,
			"disable_digg_stat": false, "disable_video_relate_book": false,
			"need_all_video_definition": true, "need_mp4_align": false,
			"use_os_player": false, "use_server_dns": false, "video_platform": 1024},
		"mixed_video_id_map": map[string]any{"1004": []string{vid}}, "dr_scene": "player",
	})
	return body
}

// Compress before signing: the server verifies the transmitted gzip bytes.
// Only the ticket is adjusted to select supported v5 digest branch 12.
func signedMobileRequest(ctx context.Context, payload []byte, dev *device, now time.Time) (*http.Request, error) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(payload); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	body := compressed.Bytes()
	q := url.Values{"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {"74332"},
		"version_name": {"7.4.3.32"}, "channel": {"vivo_8662_64"}, "device_platform": {"android"},
		"device_type": {"PGT-AN10"}, "os_version": {"12"}, "update_version_code": {"74332"},
		"iid": {dev.IID}, "cdid": {dev.CDID}, "device_id": {dev.DeviceID}}
	ts := uint32(now.Unix())
	bh := md5.Sum(body)
	for n := 0; n < 1024; n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q.Set("_rticket", strconv.FormatInt(now.UnixMilli()+int64(n), 10))
		query := strings.ReplaceAll(q.Encode(), "+", "%20")
		qh := sm3([]byte(query))
		if signingIV(signingIV(signingIV(0x20230928, qh), bh[:]), le32(ts))&15 != 12 {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, mobileVideoAPI+"?"+query, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range map[string]string{
			"User-Agent": "com.phoenix.read/73932 (Linux; U; Android 12; zh_CN_#Hans; PGT-AN10; Build/V417IR;tt-ok/3.12.13.20)",
			"Accept":     "application/json", "Content-Type": "application/json; charset=utf-8", "Content-Encoding": "gzip",
			"x-ss-stub": strings.ToUpper(hex.EncodeToString(bh[:])),
			"x-khronos": strconv.FormatUint(uint64(ts), 10),
			"x-helios":  heliosWithLicense(ts, random32(), mobileLicense),
			"x-medusa":  buildMobileMedusa(query, body, ts, dev, signingNonce, signingStarted, random32),
		} {
			req.Header.Set(k, v)
		}
		return req, nil
	}
	return nil, errors.New("无法生成播放请求签名")
}

func buildMobileMedusa(query string, body []byte, ts uint32, dev *device, nonce string, started int64, nextRandom func() uint32) string {
	bh := signingBodyHash(body)
	qh := sm3([]byte(query))
	f13 := hashF13Profile(qh, bh[:], ts, true)
	flags := make([]byte, 8)
	deviceInfo := proto(sf(1, 1), sf(2, 2), sf(3, "8662"), sf(4, dev.DeviceID),
		sf(5, nonce), sf(6, "!noperm!"), sf(12, "Asia/Shanghai,8"), sf(13, "zh_CN"),
		sf(22, "12"), sf(25, started), sf(30, "PGT-AN10"), sf(31, "HONOR"), sf(32, "PGT-AN10"), sf(34, "HONOR"))
	env := proto(sf(6, "v04.10.03.04-bugfix"), sf(8, "vivo_8662_64"), sf(12, deviceInfo), sf(14, "7.4.3.32"))
	message := proto(sf(1, mobileMagic), sf(2, 5), sf(3, nextRandom()), sf(4, "8662"),
		sf(5, dev.DeviceID), sf(6, mobileLicense), sf(7, "7.4.3.32"), sf(8, "v04.10.03-ml-android"),
		sf(9, 0x040a0300), sf(10, flags), sf(12, ts), sf(13, f13), sf(14, qh[:6]),
		sf(15, proto(sf(1, 1), sf(2, 1), sf(3, 1), sf(5, 1))), sf(17, ts), sf(20, "none"), sf(21, 377), sf(23, env),
		sf(24, `{"cmr":16777216,"cmr2":16777216,"un_h":11,"vpn":0,"kd":0,"dyn":"","do":1,"tk":true}`))
	random := nextRandom()
	hash := sm3(join(mobileSignKey, le32(random), mobileSignKey))
	high := random >> 16 & 255
	seed := le32(^((high<<11 | random>>24) ^ (high >> 5) ^ high))
	transformed := join(flags, xmxor(message, hash))
	for i, j := 0, len(transformed)-1; i < j; i, j = i+1, j-1 {
		transformed[i], transformed[j] = transformed[j], transformed[i]
	}
	for i := range transformed {
		transformed[i] ^= seed[(^i)&3]
	}
	check := uint32(0x18000001) | uint32(qh[0]&63)<<14 | uint32(f13[0]&63)<<8
	packed := join([]byte{0xca}, le32(nextRandom()), le32(check), transformed, []byte{byte(random >> 16), byte(random >> 24)})
	key, iv := md5.Sum(mobileSignKey[:16]), md5.Sum(mobileSignKey[16:])
	encrypted := newSigningCarrier(key[:], ts, true).encrypt(packed, iv[:])
	prefix := join(le32(5), mobileMagic)
	stamp := le32(ts)
	for i := range prefix {
		prefix[i] ^= stamp[i&3]
	}
	return base64.StdEncoding.EncodeToString(join(prefix, []byte{byte(random), byte(random >> 8), 0, 1}, encrypted))
}

var mobileSbox = unhex(
	"53290fe9e51f316f90f74a7e034d367784ab49237cd0135cdc632c322fd3be0e2624a417ecd9151a7ff64f60730a87f9445ec6ada2f865d22b696c4188f24c0d" +
		"f37b75510b939f6bc2fe6883e68bc8c722181ba0bdb08c9871b50c9a16b46eea8eed20ba4b8a14e0fcf1468dbbef05a93e5a9d7dcf06898635c1fd99f5de287a" +
		"80c594daaab19b6279fbd75070cb52eb2555c456e39e7839853874e2f0013ac0b64742d8ca10e4b79c955de8b843ce4ed464a6a72ed508091c92e7b3eedd1e1d" +
		"d12afac321618fa3d65f9167c982f43d66a53c6a1997585b346dbf9672ff37cdbc073b2dae11453304df00815476b2a13fccdb30b957a8acafe1274002485912" +
		"38922563e36406d37a24100b7902a028044e218453c2a399199c9d13dc40c55ba67e502273850ebdc1a2e2188b8f2d9e3cb909b8c46a2a9f4a6dd93007eafa65" +
		"b7b3a881aa908cc74f0ad76bbbb58617c37012ca05a469fd44e62eecf1e4272ce860c0ae5eadf99b20d8f0234174006f3f110d7657d01645d4eff8f71471cbfe" +
		"1cb2f52648727bf3c9de55153e031b4d61757db63dbf7782eeafe9018a8891362fb14759ac32bab03b349aeb5a377c5156be4c8dfbe13a684bda39cf836697c8" +
		"94545fe0f2491acea9fc6252f4ff1e0fe72bd2df6e67a7f635e5bcc6d1a5dd951d89960887a18eb40c785cdb1f80ed42299333ab98cdd67f316cd54346585dcc" +
		"d759fb722789b08807cc0fda293f7e5e1819bb0353517020b31d3d00374952bea2c421e862040a9ec892a05b266b7a0d0cc316f483b81af39715657ffe32ad8d" +
		"e0c13077646035236ee5363e5f900e9a7cf850a81cd9960b6df73317226f99eaa6fff15cec34acb98246639b918ab2851466a7cdfc4ad1b52c2ae95a58d3734e" +
		"47ddd5b7560193f644ef9c408b7dee39b4d6aabf4de761e3252d4c7ba3843187745d55e6a5db05d42b45caed43c0c5d0b64bfa9d94a4a9d8f2f0e1a108dcfd28" +
		"8fd2bc1fc7bd675412138169cb09249875ba3b68cfb11180afae104fc9789571c6dfce02413886796ae21e3c488cc21b9f423a066c2eabf98e2fdeeb5776e4f5" +
		"d09a515ba58cbbab3e37b09fd4f6d9244df80512e84cd7956be301cf64f1429d58577cc241f3a2c655144f81aaf95a4e108da894b38970561ae9002619fbbac9" +
		"1fa922ed202bfc62cb08e0de2a7f3631435f8b47c8a3c563b4db4071ce74c46da6fa696a9e282d1e825d663b93bfd56c48dce1884bb10fccea063929f746443f" +
		"d830a4232e53da21a1f2d1bdfebc3a02c10a1b8ad31d04fd616590d272ac737e98eb33af60528716bee78ec0ee8699031177277d9cff32542c67caf5f4459168" +
		"1335ae0e178f3409a7803db83892dde5b9b7f0c750e24aef077badd678a09b850b84ec1597e4963cb5e675df1c0d6fb618496ecd76c35c798325590c2fb27a5e")

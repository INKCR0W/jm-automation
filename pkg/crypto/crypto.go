package crypto

import (
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// 加密相关的密钥常量
const (
	AppTokenSecret        = "18comicAPP"
	AppTokenSecret2       = "18comicAPPContent" // 用于特殊接口
	AppDataSecret         = "185Hcomic3PAPP7R"
	APIDomainServerSecret = "diosfjckwpqpdfjkvnqQjsik"
)

// MD5Hex 计算字符串的 MD5 哈希值并返回十六进制字符串
func MD5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// TokenAndTokenParam 计算请求头的 token 和 tokenparam
// ts: 时间戳（秒）
// version: APP 版本号
// secret: 密钥（可选，默认使用 AppTokenSecret）
func TokenAndTokenParam(ts int64, version string, secret ...string) (string, string) {
	sec := AppTokenSecret
	if len(secret) > 0 {
		sec = secret[0]
	}

	// tokenparam: "1700566805,2.0.16"
	tokenparam := fmt.Sprintf("%d,%s", ts, version)

	// token: md5(ts + secret)
	token := MD5Hex(fmt.Sprintf("%d%s", ts, sec))

	return token, tokenparam
}

// DecodeRespData 解密 API 响应数据
// data: base64 编码的加密数据
// ts: 时间戳（必须与请求时使用的时间戳一致）
// secret: 密钥（可选，默认使用 AppDataSecret）
func DecodeRespData(data string, ts int64, secret ...string) (string, error) {
	sec := AppDataSecret
	if len(secret) > 0 {
		sec = secret[0]
	}

	// 1. Base64 解码
	ciphertext, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", fmt.Errorf("base64 decode failed: %w", err)
	}

	// 2. 生成 AES 密钥
	key := MD5Hex(fmt.Sprintf("%d%s", ts, sec))

	// 3. AES-ECB 解密
	plaintext, err := aesECBDecrypt(ciphertext, []byte(key))
	if err != nil {
		return "", fmt.Errorf("aes decrypt failed: %w", err)
	}

	// 4. 移除 PKCS7 padding
	plaintext = removePKCS7Padding(plaintext)

	return string(plaintext), nil
}

// aesECBDecrypt AES-ECB 模式解密
// Go 标准库不直接支持 ECB 模式，需要手动实现
func aesECBDecrypt(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext is not a multiple of the block size")
	}

	plaintext := make([]byte, len(ciphertext))

	// ECB 模式：每个块独立解密
	for i := 0; i < len(ciphertext); i += aes.BlockSize {
		block.Decrypt(plaintext[i:i+aes.BlockSize], ciphertext[i:i+aes.BlockSize])
	}

	return plaintext, nil
}

// removePKCS7Padding 移除 PKCS7 填充
func removePKCS7Padding(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	// 最后一个字节表示填充的长度
	padding := int(data[len(data)-1])

	// 验证填充是否有效
	if padding > len(data) || padding > aes.BlockSize {
		return data
	}

	// 检查填充字节是否都相同
	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return data
		}
	}

	return data[:len(data)-padding]
}

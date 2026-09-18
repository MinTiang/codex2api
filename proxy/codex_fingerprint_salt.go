package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// 每部署随机盐：混入 Codex 收敛身份（installation_id / session / thread / window）
// 的全部确定性派生哈希。
//
// 背景：确定性派生保证了「同一种子恒定同值、无需落库」，但也意味着同一开源项目
// 的所有部署共享同一套 派生输入 → 派生输出 映射。账号数一多，上游可以把这些部署
// 的收敛身份聚类成同一个「非官方客户端群体」——单个部署伪装得再像，群体特征是
// 一眼可辨的。盐让每个部署的派生空间彼此独立：不同部署对同一账号 ID 派生出完全
// 不同的标识，群体聚类失去锚点。
//
// 盐的持久化：收敛身份的契约是「跨进程重启不变」（installation_id 每次重启都换
// 等于设备不断重装），所以盐必须落盘，而不是每次启动随机。落盘位置优先数据库
// 同目录（SQLite 部署即 /data 卷，随数据一起备份迁移），不可写时退到系统临时目录，
// 都失败时退化为进程内随机（仅损失重启稳定性，不影响请求正确性）。
//
// 覆盖方式：CODEX_FINGERPRINT_SALT 显式指定（跨机迁移时保持身份一致的运维出口）。

const (
	codexFingerprintSaltFile     = "fingerprint-salt.key"
	codexFingerprintSaltMinChars = 16
)

var (
	codexFingerprintSaltOnce sync.Once
	codexFingerprintSaltVal  atomic.Value // string
)

// CodexFingerprintSalt 返回本部署的派生盐。首次调用时确定，之后进程内恒定。
func CodexFingerprintSalt() string {
	codexFingerprintSaltOnce.Do(func() {
		codexFingerprintSaltVal.Store(loadOrCreateCodexFingerprintSalt())
	})
	value, _ := codexFingerprintSaltVal.Load().(string)
	return value
}

func loadOrCreateCodexFingerprintSalt() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_FINGERPRINT_SALT")); len(value) >= codexFingerprintSaltMinChars {
		return value
	}
	for _, dir := range codexFingerprintSaltCandidateDirs() {
		path := filepath.Join(dir, codexFingerprintSaltFile)
		if data, err := os.ReadFile(path); err == nil {
			if value := strings.TrimSpace(string(data)); len(value) >= codexFingerprintSaltMinChars {
				return value
			}
		}
		value, err := randomCodexFingerprintSalt()
		if err != nil {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		// 0600：盐等价于账号身份的种子，不应对其它用户可读。
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			continue
		}
		return value
	}
	log.Printf("codex指纹盐落盘失败，退化为进程内随机盐（重启后收敛身份会变化）")
	value, err := randomCodexFingerprintSalt()
	if err != nil {
		return "codex2api:fallback-salt"
	}
	return value
}

// codexFingerprintSaltCandidateDirs 返回盐文件的候选目录：SQLite 数据库所在目录
// （标准持久化卷）优先，其后是系统临时目录兜底。
func codexFingerprintSaltCandidateDirs() []string {
	var dirs []string
	if dbPath := strings.TrimSpace(os.Getenv("DATABASE_PATH")); dbPath != "" && dbPath != ":memory:" {
		if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
			dirs = append(dirs, dir)
		}
	}
	if tmp := os.TempDir(); tmp != "" {
		dirs = append(dirs, tmp)
	}
	return dirs
}

func randomCodexFingerprintSalt() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

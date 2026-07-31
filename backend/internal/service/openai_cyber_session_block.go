package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// CyberSessionBlockStore 是 cyber 会话屏蔽表的存取接口。
// repository 层 gatewayCache 附带实现（类型断言探测接入，不改 GatewayCache
// 共享接口）；测试 stub 不实现时屏蔽能力自动降级关闭。
type CyberSessionBlockStore interface {
	SetCyberSessionBlocked(ctx context.Context, key string, ttl time.Duration) error
	IsCyberSessionBlocked(ctx context.Context, key string) (bool, error)
}

const policySessionWriteTimeout = 2 * time.Second

// KeywordSessionBlockStore stores terminal local keyword-policy blocks in a
// namespace separate from cyber-policy state.
type KeywordSessionBlockStore interface {
	ClaimKeywordSessionBlocked(ctx context.Context, key string, ttl time.Duration) (bool, error)
	IsKeywordSessionBlocked(ctx context.Context, key string) (bool, error)
}

// OpenAISessionBlockKey derives a policy-block key only from explicit session
// identifiers. It never falls back to API-key-wide or content-derived blocking.
func OpenAISessionBlockKey(apiKeyID int64, c *gin.Context, body []byte) string {
	raw := explicitOpenAITerminalSessionID(c, body)
	if raw == "" {
		return ""
	}
	isolated := isolateOpenAISessionID(apiKeyID, raw)
	sum := sha256.Sum256([]byte(isolated))
	return hex.EncodeToString(sum[:])
}

func CyberSessionBlockKey(apiKeyID int64, c *gin.Context, body []byte) string {
	return OpenAISessionBlockKey(apiKeyID, c, body)
}

// CyberPolicyBlockKey preserves explicit-session blocking when an identity is
// present. Stateless requests fall back to the normalized text of their latest
// user message, isolated by API key and protocol. Empty or non-textual inputs
// remain fail-open.
func CyberPolicyBlockKey(apiKeyID int64, c *gin.Context, protocol string, body []byte) string {
	if sessionKey := CyberSessionBlockKey(apiKeyID, c, body); sessionKey != "" {
		return sessionKey
	}
	text := ExtractLastUserMessageText(protocol, body)
	if text == "" {
		return ""
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("cyber-last-user:v1\x00"))
	_, _ = hash.Write([]byte(strconv.FormatInt(apiKeyID, 10)))
	_, _ = hash.Write([]byte{'\x00'})
	_, _ = hash.Write([]byte(protocol))
	_, _ = hash.Write([]byte{'\x00'})
	_, _ = hash.Write([]byte(text))
	return "message:v1:" + hex.EncodeToString(hash.Sum(nil))
}

func OpenAIKeywordSessionBlockKey(sessionKey, policyVersion string) string {
	if sessionKey == "" || policyVersion == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionKey + ":" + policyVersion))
	return hex.EncodeToString(sum[:])
}

// cyberSessionBlockStore 探测 cache 是否具备屏蔽存储能力。
// 注意：若未来以装饰器包装 GatewayCache（如日志/指标装饰器），该装饰器必须同时实现
// CyberSessionBlockStore，否则会话屏蔽能力将静默降级关闭
// （编译断言 var _ service.CyberSessionBlockStore = (*gatewayCache)(nil) 只覆盖
// *gatewayCache 本体，无法覆盖其外层包装）。
func (s *OpenAIGatewayService) cyberSessionBlockStore() CyberSessionBlockStore {
	if s == nil || s.cache == nil {
		return nil
	}
	store, ok := s.cache.(CyberSessionBlockStore)
	if !ok {
		return nil
	}
	return store
}

func (s *OpenAIGatewayService) keywordSessionBlockStore() KeywordSessionBlockStore {
	if s == nil || s.cache == nil {
		return nil
	}
	store, ok := s.cache.(KeywordSessionBlockStore)
	if !ok {
		return nil
	}
	return store
}

// CyberSessionBlockRuntime 返回 (开关, TTL)。开关默认关。
// 委托给 SettingService.GetCyberSessionBlockRuntime，进程内缓存避免热路径 DB 往返。
func (s *OpenAIGatewayService) CyberSessionBlockRuntime(ctx context.Context) (bool, time.Duration) {
	if s == nil || s.settingService == nil {
		return false, time.Hour
	}
	return s.settingService.GetCyberSessionBlockRuntime(ctx)
}

// MarkCyberSessionBlocked 把会话写入屏蔽表（写入点：cyber 命中后）。
// 开关关闭、key 为空或存储不可用时静默跳过。
func (s *OpenAIGatewayService) MarkCyberSessionBlocked(ctx context.Context, key string) {
	if key == "" {
		return
	}
	enabled, ttl := s.CyberSessionBlockRuntime(ctx)
	if !enabled {
		return
	}
	store := s.cyberSessionBlockStore()
	if store == nil {
		return
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), policySessionWriteTimeout)
	defer cancel()
	if err := store.SetCyberSessionBlocked(persistCtx, key, ttl); err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session block write failed: err=%v", err)
	}
}

// IsCyberSessionBlocked 查询会话是否被屏蔽（拦截点）。开关关闭、key 为空、
// 存储不可用或查询出错时返回 false（fail-open：屏蔽是增强防护，不阻断主链路）。
func (s *OpenAIGatewayService) IsCyberSessionBlocked(ctx context.Context, key string) bool {
	if key == "" {
		return false
	}
	enabled, _ := s.CyberSessionBlockRuntime(ctx)
	if !enabled {
		return false
	}
	store := s.cyberSessionBlockStore()
	if store == nil {
		return false
	}
	blocked, err := store.IsCyberSessionBlocked(ctx, key)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session block read failed: err=%v", err)
		return false
	}
	return blocked
}

func (s *OpenAIGatewayService) IsKeywordSessionBlocked(ctx context.Context, key string) bool {
	if key == "" {
		return false
	}
	store := s.keywordSessionBlockStore()
	if store == nil {
		return false
	}
	blocked, err := store.IsKeywordSessionBlocked(ctx, key)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "keyword session block read failed: err=%v", err)
		return false
	}
	return blocked
}

// ClaimKeywordSessionBlocked atomically creates a terminal block before the
// winning request records its local keyword hit. A false result means another
// request already claimed the same policy-scoped session.
func (s *OpenAIGatewayService) ClaimKeywordSessionBlocked(ctx context.Context, key string) (claimed bool, available bool) {
	if key == "" {
		return false, false
	}
	store := s.keywordSessionBlockStore()
	if store == nil {
		return false, false
	}
	_, ttl := s.CyberSessionBlockRuntime(ctx)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), policySessionWriteTimeout)
	defer cancel()
	claimed, err := store.ClaimKeywordSessionBlocked(persistCtx, key, ttl)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "keyword session block claim failed: err=%v", err)
		return false, false
	}
	return claimed, true
}

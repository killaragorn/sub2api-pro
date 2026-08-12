package handler

import (
	"crypto/sha256"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const securityAuditCompletedContextKey = "sub2api.security_audit.completed"
const keywordSessionBlockKeyContextKey = "sub2api.keyword_session_block.key"
const securityAuditWSTurnContextKey = "sub2api.security_audit.ws_turn"
const securityAuditWSDedupeContextKey = "sub2api.security_audit.ws_dedupe"

const keywordSessionBlockedClientMsg = "该会话已被关键词策略屏蔽，请开启新会话 / This session is blocked by keyword policy, please start a new session"

type securityAuditWSDedupeEntry struct {
	stage    string
	turn     int
	bodyHash [sha256.Size]byte
	decision securityaudit.Decision
}

// cachesSecurityAuditCompletion reports whether a successful audit may be
// reused for the rest of the gin request. WebSocket turns share one Context
// across many response.create frames and must be audited independently.
func cachesSecurityAuditCompletion(stage string) bool {
	switch strings.TrimSpace(stage) {
	case "", "http":
		return true
	default:
		return false
	}
}

func isSecurityAuditWebSocketStage(stage string) bool {
	switch strings.TrimSpace(stage) {
	case "first_turn", "subsequent_turn":
		return true
	default:
		return false
	}
}

func (h *GatewayHandler) checkSecurityAudit(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol, model string, body []byte) *securityaudit.Decision {
	if h == nil {
		return nil
	}
	return runSecurityAudit(c, reqLog, h.securityAuditCoordinator, h.contentModerationService, apiKey, subject, protocol, model, body, "http")
}

func (h *OpenAIGatewayHandler) checkSecurityAudit(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol, model string, body []byte) *securityaudit.Decision {
	return h.checkOpenAISecurityAuditStage(c, reqLog, apiKey, subject, protocol, model, body, "http")
}

func (h *OpenAIGatewayHandler) checkSecurityAuditStage(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol, model string, body []byte, stage string) *securityaudit.Decision {
	return h.checkOpenAISecurityAuditStage(c, reqLog, apiKey, subject, protocol, model, body, stage)
}

func (h *OpenAIGatewayHandler) checkOpenAISecurityAuditStage(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol, model string, body []byte, stage string) *securityaudit.Decision {
	if h == nil {
		return nil
	}
	blockKey := ""
	var keywordScan *service.ContentModerationKeywordScan
	if keywordSessionBlockingProtocol(protocol) && h.gatewayService != nil && h.contentModerationService != nil &&
		apiKey != nil && c != nil && c.Request != nil {
		sessionKey := service.OpenAISessionBlockKey(apiKey.ID, c, body)
		if sessionKey != "" {
			c.Set(keywordSessionBlockKeyContextKey, sessionKey)
		} else if cached, ok := c.Get(keywordSessionBlockKeyContextKey); ok {
			sessionKey, _ = cached.(string)
		}
		input := buildContentModerationInput(c, apiKey, subject, protocol, model, body)
		policy := h.contentModerationService.KeywordSessionPolicy(c.Request.Context(), input)
		if policy.Active {
			keywordScan = policy.Scan
			blockKey = service.OpenAIKeywordSessionBlockKey(sessionKey, policy.Version)
			if h.gatewayService.IsKeywordSessionBlocked(c.Request.Context(), blockKey) {
				return keywordSessionBlockedDecision(policy.ErrorCode)
			}
			if policy.WouldBlock {
				claimed, available := h.gatewayService.ClaimKeywordSessionBlocked(c.Request.Context(), blockKey)
				if available && !claimed {
					return keywordSessionBlockedDecision(policy.ErrorCode)
				}
			}
		}
	}

	return runSecurityAuditWithScan(c, reqLog, h.securityAuditCoordinator, h.contentModerationService, apiKey, subject, protocol, model, body, stage, keywordScan)
}

func keywordSessionBlockingProtocol(protocol string) bool {
	switch strings.TrimSpace(protocol) {
	case service.ContentModerationProtocolOpenAIChat,
		service.ContentModerationProtocolOpenAIResponses,
		service.ContentModerationProtocolOpenAICodexMemory:
		return true
	default:
		return false
	}
}

func isKeywordBlockDecision(decision *securityaudit.Decision) bool {
	return decision != nil && decision.Legacy != nil && decision.Legacy.Blocked &&
		decision.Legacy.Action == service.ContentModerationActionKeywordBlock
}

func isKeywordContentPolicyDecision(decision *securityaudit.Decision) bool {
	return isKeywordBlockDecision(decision) ||
		(decision != nil && decision.ClientMessage == keywordSessionBlockedClientMsg)
}

func keywordSessionBlockedDecision(errorCode string) *securityaudit.Decision {
	if errorCode == "" {
		errorCode = service.DefaultContentModerationBlockErrorCode()
	}
	return &securityaudit.Decision{
		Kind:           securityaudit.DecisionBlock,
		HTTPStatus:     http.StatusBadRequest,
		ErrorCode:      errorCode,
		ClientMessage:  keywordSessionBlockedClientMsg,
		AllowNextStage: false,
	}
}

func runSecurityAudit(c *gin.Context, reqLog *zap.Logger, coordinator *securityaudit.Coordinator, legacy *service.ContentModerationService, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol, model string, body []byte, stage string) *securityaudit.Decision {
	return runSecurityAuditWithScan(c, reqLog, coordinator, legacy, apiKey, subject, protocol, model, body, stage, nil)
}

func runSecurityAuditWithScan(c *gin.Context, reqLog *zap.Logger, coordinator *securityaudit.Coordinator, legacy *service.ContentModerationService, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol, model string, body []byte, stage string, keywordScan *service.ContentModerationKeywordScan) *securityaudit.Decision {
	if c == nil || c.Request == nil {
		return nil
	}
	cacheCompletion := cachesSecurityAuditCompletion(stage)
	if cacheCompletion {
		if completed, exists := c.Get(securityAuditCompletedContextKey); exists && completed == true {
			return nil
		}
	}
	if coordinator == nil {
		legacyDecision := runContentModeration(c, reqLog, legacy, apiKey, subject, protocol, model, body, keywordScan)
		if legacyDecision == nil {
			return nil
		}
		legacyErrorCode := legacyDecision.ErrorCode
		if legacyErrorCode == "" {
			legacyErrorCode = service.DefaultContentModerationBlockErrorCode()
		}
		decision := securityaudit.Decision{Kind: securityaudit.DecisionAllow, HTTPStatus: http.StatusOK, AllowNextStage: true}
		decision.Legacy = &securityaudit.LegacyDecision{
			Allowed: legacyDecision.Allowed, Blocked: legacyDecision.Blocked, Flagged: legacyDecision.Flagged,
			Message: legacyDecision.Message, StatusCode: legacyDecision.StatusCode,
			ErrorCode: legacyErrorCode, Action: legacyDecision.Action,
		}
		if legacyDecision.Blocked {
			decision.Kind, decision.HTTPStatus, decision.ErrorCode, decision.ClientMessage, decision.AllowNextStage = securityaudit.DecisionBlock, contentModerationStatus(legacyDecision), legacyErrorCode, legacyDecision.Message, false
		}
		if decision.AllowNextStage && cacheCompletion {
			c.Set(securityAuditCompletedContextKey, true)
		}
		return &decision
	}
	request := buildSecurityAuditRequest(c, apiKey, subject, protocol, model, body, stage)
	request.KeywordScan = keywordScan
	if isSecurityAuditWebSocketStage(request.Stage) {
		if turnNo, ok := securityAuditWSTurn(c); ok {
			bodyHash := sha256.Sum256(body)
			if cached, exists := c.Get(securityAuditWSDedupeContextKey); exists {
				if entry, ok := cached.(securityAuditWSDedupeEntry); ok &&
					entry.stage == request.Stage && entry.turn == turnNo && entry.bodyHash == bodyHash {
					decision := entry.decision
					logSecurityAuditDone(reqLog, request, decision, true)
					return &decision
				}
			}
			logSecurityAuditStart(reqLog, request, len(body), false)
			decision := coordinator.Check(c.Request.Context(), request)
			if decision.Kind == securityaudit.DecisionAllow {
				c.Set(securityAuditWSDedupeContextKey, securityAuditWSDedupeEntry{
					stage: request.Stage, turn: turnNo, bodyHash: bodyHash, decision: decision,
				})
			}
			logSecurityAuditDone(reqLog, request, decision, false)
			return &decision
		}
	}
	logSecurityAuditStart(reqLog, request, len(body), false)
	decision := coordinator.Check(c.Request.Context(), request)
	if decision.AllowNextStage && cacheCompletion {
		c.Set(securityAuditCompletedContextKey, true)
	}
	logSecurityAuditDone(reqLog, request, decision, false)
	return &decision
}

func logSecurityAuditStart(reqLog *zap.Logger, request securityaudit.Request, bodyBytes int, cached bool) {
	if reqLog == nil {
		return
	}
	reqLog.Info("security_audit.gateway_check_start",
		zap.String("request_id", request.RequestID), zap.Int64("user_id", request.UserID),
		zap.Int64("api_key_id", request.APIKeyID), zap.Int64p("group_id", request.GroupID),
		zap.String("endpoint", request.Endpoint), zap.String("provider", request.Provider),
		zap.String("protocol", request.Protocol), zap.String("model", request.Model), zap.String("stage", request.Stage),
		zap.Int("body_bytes", bodyBytes), zap.Bool("cached", cached))
}

func logSecurityAuditDone(reqLog *zap.Logger, request securityaudit.Request, decision securityaudit.Decision, cached bool) {
	if reqLog == nil {
		return
	}
	reqLog.Info("security_audit.gateway_check_done",
		zap.String("request_id", request.RequestID), zap.String("decision", string(decision.Kind)),
		zap.String("error_code", decision.ErrorCode), zap.Bool("allow_next_stage", decision.AllowNextStage),
		zap.String("stage", request.Stage), zap.Bool("cached", cached))
}

func securityAuditWSTurn(c *gin.Context) (int, bool) {
	turn, exists := c.Get(securityAuditWSTurnContextKey)
	if !exists {
		return 0, false
	}
	turnNo, ok := turn.(int)
	return turnNo, ok
}

func buildSecurityAuditRequest(c *gin.Context, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol, model string, body []byte, stage string) securityaudit.Request {
	legacy := buildContentModerationInput(c, apiKey, subject, protocol, model, body)
	request := securityaudit.Request{
		RequestID: legacy.RequestID, UserID: legacy.UserID, UserEmail: legacy.UserEmail,
		APIKeyID: legacy.APIKeyID, APIKeyName: legacy.APIKeyName, GroupID: cloneSecurityAuditGroupID(legacy.GroupID),
		GroupName: legacy.GroupName, Provider: legacy.Provider, Endpoint: legacy.Endpoint,
		Protocol: legacy.Protocol, Model: legacy.Model, Body: body, Stage: strings.TrimSpace(stage),
	}
	if apiKey != nil && apiKey.User != nil {
		request.Username = apiKey.User.Username
		if request.UserEmail == "" {
			request.UserEmail = apiKey.User.Email
		}
	}
	if request.Stage == "" {
		request.Stage = "http"
	}
	return request
}

func securityAuditStatus(decision *securityaudit.Decision) int {
	if decision == nil || decision.HTTPStatus < 400 || decision.HTTPStatus > 599 {
		return http.StatusForbidden
	}
	return decision.HTTPStatus
}

func securityAuditErrorCode(decision *securityaudit.Decision) string {
	if decision == nil || strings.TrimSpace(decision.ErrorCode) == "" {
		return "content_policy_violation"
	}
	return decision.ErrorCode
}

func securityAuditMessage(decision *securityaudit.Decision) string {
	if decision == nil {
		return "Request blocked by content policy"
	}
	if decision.Legacy != nil && decision.Legacy.Blocked && strings.TrimSpace(decision.Legacy.Message) != "" {
		return decision.Legacy.Message
	}
	if strings.TrimSpace(decision.ClientMessage) != "" {
		return decision.ClientMessage
	}
	return "Request blocked by content policy"
}

func cloneSecurityAuditGroupID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

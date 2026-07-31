package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	openAICodexUpstreamIdentityContextKey = "openai_codex_upstream_identity"
	openAIOfficialThreadIDHeader          = "thread-id"
	openAIOfficialClientRequestIDHeader   = "x-client-request-id"
	openAICodexParentThreadIDHeader       = "x-codex-parent-thread-id"
)

type openAICodexUpstreamIdentity struct {
	InstallationID     string
	SessionID          string
	ThreadID           string
	ForkedFromThreadID string
	ParentThreadID     string
	WindowID           string
	TurnMetadata       string
}

type openAICodexIdentityBodyOptions struct {
	Compact              bool
	CreateClientMetadata bool
}

type openAICodexPromptCacheOptimizationResult struct {
	Body        []byte
	RequestView openAIRequestView
	CodexResult codexTransformResult
	Applied     bool
}

func (s *OpenAIGatewayService) openAICodexPromptCacheOptimizationEnabled(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if s.settingService != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		return s.settingService.IsOpenAICodexPromptCacheOptimizationEnabled(ctx)
	}
	return s.cfg != nil && s.cfg.Gateway.OpenAICodexPromptCacheOptimizationEnabled
}

// tryApplyOpenAICodexPromptCacheOptimization owns the complete optional entry
// point. Keeping eligibility, pending-patch application, and identity reset in
// this file limits the upstream forwarding integration to a single hook.
func (s *OpenAIGatewayService) tryApplyOpenAICodexPromptCacheOptimization(
	c *gin.Context,
	account *Account,
	body []byte,
	requestView openAIRequestView,
	bodyAlreadyDecoded bool,
	isOfficialCodexClient bool,
	compatMessagesBridge bool,
	isCompactRequest bool,
) (openAICodexPromptCacheOptimizationResult, error) {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if !s.openAICodexPromptCacheOptimizationEnabled(ctx) {
		return openAICodexPromptCacheOptimizationResult{}, nil
	}
	if !isOfficialCodexClient ||
		compatMessagesBridge ||
		isCompactRequest ||
		bodyAlreadyDecoded ||
		requestView.patchesDisabled {
		return openAICodexPromptCacheOptimizationResult{}, nil
	}

	workingBody := body
	if requestView.HasPatches() {
		patchedBody, err := requestView.ApplyPatches()
		if err != nil {
			return openAICodexPromptCacheOptimizationResult{}, err
		}
		workingBody = patchedBody
	}

	transformedBody, codexResult, handled, err := tryTransformNativeCodexOAuthRequest(c, account, workingBody)
	if err != nil {
		return openAICodexPromptCacheOptimizationResult{}, err
	}
	if !handled {
		return openAICodexPromptCacheOptimizationResult{}, nil
	}
	return openAICodexPromptCacheOptimizationResult{
		Body:        transformedBody,
		RequestView: newOpenAIRequestView(transformedBody),
		CodexResult: codexResult,
		Applied:     true,
	}, nil
}

// tryTransformNativeCodexOAuthRequest keeps an official Codex Responses payload
// on a raw-JSON patch path. Native Codex already emits the schema accepted by
// ChatGPT's internal Responses endpoint, so decoding and re-marshalling stable
// instructions, tools, input, and text only creates unnecessary prompt churn.
//
// handled is false when the request needs one of the compatibility conversions
// owned by applyCodexOAuthTransformWithOptions.
func tryTransformNativeCodexOAuthRequest(
	c *gin.Context,
	account *Account,
	body []byte,
) (transformed []byte, result codexTransformResult, handled bool, err error) {
	root := parseRawJSONView(body)
	if !canPatchNativeCodexOAuthRequest(root) {
		return body, codexTransformResult{}, false, nil
	}

	result.NormalizedModel = strings.TrimSpace(root.Get("model").String())
	rawPromptCacheKey := strings.TrimSpace(root.Get("prompt_cache_key").String())
	if rawPromptCacheKey == "" && c != nil && c.Request != nil {
		rawPromptCacheKey = strings.TrimSpace(c.Request.Header.Get(openAIOfficialSessionIDHeader))
		if rawPromptCacheKey == "" {
			rawPromptCacheKey = strings.TrimSpace(c.Request.Header.Get("session_id"))
		}
	}
	if rawPromptCacheKey == "" {
		rawPromptCacheKey = strings.TrimSpace(root.Get("client_metadata.session_id").String())
	}
	result.PromptCacheKey = rawPromptCacheKey

	transformed = body
	if model := root.Get("model"); model.Type == gjson.String && model.String() != result.NormalizedModel {
		transformed, err = sjson.SetBytes(transformed, "model", result.NormalizedModel)
		if err != nil {
			return body, codexTransformResult{}, false, fmt.Errorf("patch native Codex model: %w", err)
		}
	}

	if store := gjson.GetBytes(transformed, "store"); !store.Exists() || store.Type != gjson.False {
		transformed, err = sjson.SetBytes(transformed, "store", false)
		if err != nil {
			return body, codexTransformResult{}, false, fmt.Errorf("patch native Codex store: %w", err)
		}
	}
	if stream := gjson.GetBytes(transformed, "stream"); !stream.Exists() || stream.Type != gjson.True {
		transformed, err = sjson.SetBytes(transformed, "stream", true)
		if err != nil {
			return body, codexTransformResult{}, false, fmt.Errorf("patch native Codex stream: %w", err)
		}
	}

	for _, field := range openAICodexOAuthUnsupportedFields {
		if !gjson.GetBytes(transformed, field).Exists() {
			continue
		}
		transformed, err = sjson.DeleteBytes(transformed, field)
		if err != nil {
			return body, codexTransformResult{}, false, fmt.Errorf("delete native Codex field %s: %w", field, err)
		}
	}

	transformed, err = patchNativeCodexReasoningInclude(transformed)
	if err != nil {
		return body, codexTransformResult{}, false, err
	}
	transformed, err = patchNativeCodexInput(transformed)
	if err != nil {
		return body, codexTransformResult{}, false, err
	}

	identity, hasIdentity := openAICodexUpstreamIdentityFromContext(c)
	if !hasIdentity {
		apiKeyID := getAPIKeyIDFromContext(c)
		identity = resolveOpenAICodexUpstreamIdentity(c, account, transformed, apiKeyID, false)
	}
	transformed, err = patchOpenAICodexIdentityBody(
		transformed,
		account,
		identity,
		openAICodexIdentityBodyOptions{CreateClientMetadata: true},
	)
	if err != nil {
		return body, codexTransformResult{}, false, err
	}
	if c != nil && identity != (openAICodexUpstreamIdentity{}) {
		c.Set(openAICodexUpstreamIdentityContextKey, identity)
	}

	result.Modified = !bytes.Equal(body, transformed)
	return transformed, result, true, nil
}

func canPatchNativeCodexOAuthRequest(root gjson.Result) bool {
	if !root.IsObject() {
		return false
	}
	model := root.Get("model")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" ||
		isCodexSparkModel(strings.TrimSpace(model.String())) {
		return false
	}
	instructions := root.Get("instructions")
	if instructions.Type != gjson.String || strings.TrimSpace(instructions.String()) == "" {
		return false
	}
	if root.Get("functions").Exists() || root.Get("function_call").Exists() {
		return false
	}
	if clientMetadata := root.Get("client_metadata"); clientMetadata.Exists() && !clientMetadata.IsObject() {
		return false
	}

	tools := root.Get("tools")
	if !tools.IsArray() || len(tools.Array()) == 0 {
		return false
	}
	toolsCompatible := true
	tools.ForEach(func(_, tool gjson.Result) bool {
		if !tool.IsObject() || strings.TrimSpace(tool.Get("type").String()) != "function" {
			return true
		}
		if strings.TrimSpace(tool.Get("name").String()) == "" || tool.Get("function").Exists() {
			toolsCompatible = false
			return false
		}
		return true
	})
	if !toolsCompatible || nativeCodexToolChoiceNeedsNormalization(root) {
		return false
	}

	input := root.Get("input")
	if !input.IsArray() {
		return false
	}
	inputCompatible := true
	input.ForEach(func(_, item gjson.Result) bool {
		if !item.IsObject() {
			return true
		}
		switch strings.TrimSpace(item.Get("role").String()) {
		case "system", "tool":
			inputCompatible = false
			return false
		}
		if strings.TrimSpace(item.Get("type").String()) != "message" {
			return true
		}
		content := item.Get("content")
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(_, part gjson.Result) bool {
			text := part.Get("text")
			if text.Exists() && text.Type != gjson.String {
				inputCompatible = false
				return false
			}
			return true
		})
		return inputCompatible
	})
	return inputCompatible
}

func nativeCodexToolChoiceNeedsNormalization(root gjson.Result) bool {
	choice := root.Get("tool_choice")
	if !choice.Exists() || !choice.IsObject() {
		return false
	}
	choiceType := strings.TrimSpace(choice.Get("type").String())
	if choiceType == "" {
		return false
	}
	if choiceType == "function" {
		name := strings.TrimSpace(choice.Get("name").String())
		if name == "" || choice.Get("function").Exists() {
			return true
		}
		return !nativeCodexToolsContain(root.Get("tools"), "function", name)
	}
	if nativeCodexToolsContain(root.Get("tools"), choiceType, "") {
		return false
	}
	additionalTools := root.Get("input.#(type==\"additional_tools\")#.tools")
	return !nativeCodexToolsContain(additionalTools, choiceType, "")
}

func nativeCodexToolsContain(tools gjson.Result, toolType, functionName string) bool {
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, tool gjson.Result) bool {
		if strings.TrimSpace(tool.Get("type").String()) != toolType {
			return true
		}
		if functionName != "" && strings.TrimSpace(tool.Get("name").String()) != functionName {
			return true
		}
		found = true
		return false
	})
	return found
}

func patchNativeCodexReasoningInclude(body []byte) ([]byte, error) {
	reasoning := gjson.GetBytes(body, "reasoning")
	hasReasoningField := false
	if reasoning.IsObject() {
		reasoning.ForEach(func(_, _ gjson.Result) bool {
			hasReasoningField = true
			return false
		})
	}
	if !hasReasoningField {
		return body, nil
	}
	const encryptedContent = "reasoning.encrypted_content"
	include := gjson.GetBytes(body, "include")
	if !include.Exists() || include.Type == gjson.Null {
		next, err := sjson.SetBytes(body, "include", []string{encryptedContent})
		if err != nil {
			return body, fmt.Errorf("set native Codex reasoning include: %w", err)
		}
		return next, nil
	}
	if !include.IsArray() {
		return body, nil
	}
	for _, value := range include.Array() {
		if value.Type == gjson.String && value.String() == encryptedContent {
			return body, nil
		}
	}
	next, err := sjson.SetBytes(body, "include.-1", encryptedContent)
	if err != nil {
		return body, fmt.Errorf("append native Codex reasoning include: %w", err)
	}
	return next, nil
}

func patchNativeCodexInput(body []byte) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, nil
	}

	items := make([][]byte, 0, len(input.Array()))
	changed := false
	index := 0
	var patchErr error
	input.ForEach(func(_, item gjson.Result) bool {
		currentIndex := index
		index++
		itemBody := []byte(item.Raw)
		if !item.IsObject() {
			items = append(items, itemBody)
			return true
		}

		itemType := strings.TrimSpace(item.Get("type").String())
		switch itemType {
		case "reasoning":
			if item.Get("id").Exists() {
				itemBody, patchErr = sjson.DeleteBytes(itemBody, "id")
				if patchErr != nil {
					patchErr = fmt.Errorf("delete input.%d reasoning id: %w", currentIndex, patchErr)
					return false
				}
				changed = true
			}
			summary := item.Get("summary")
			if !summary.Exists() || summary.Type == gjson.Null {
				itemBody, patchErr = sjson.SetBytes(itemBody, "summary", []any{})
				if patchErr != nil {
					patchErr = fmt.Errorf("set input.%d reasoning summary: %w", currentIndex, patchErr)
					return false
				}
				changed = true
			}
			items = append(items, itemBody)
			return true
		case "item_reference":
			id := item.Get("id")
			if id.Type == gjson.String && strings.HasPrefix(id.String(), "call_") {
				itemBody, patchErr = sjson.SetBytes(itemBody, "id", normalizeCodexCallID(id.String()))
				if patchErr != nil {
					patchErr = fmt.Errorf("normalize input.%d item reference: %w", currentIndex, patchErr)
					return false
				}
				changed = true
			}
			items = append(items, itemBody)
			return true
		}

		if isCodexToolCallItemType(itemType) {
			callID := item.Get("call_id").String()
			if strings.TrimSpace(callID) == "" {
				callID = item.Get("id").String()
				if strings.TrimSpace(callID) != "" {
					itemBody, patchErr = sjson.SetBytes(itemBody, "call_id", callID)
					if patchErr != nil {
						patchErr = fmt.Errorf("set input.%d call_id: %w", currentIndex, patchErr)
						return false
					}
					changed = true
				}
			}
			if callID != "" {
				normalizedCallID := normalizeCodexCallID(callID)
				if normalizedCallID != callID {
					itemBody, patchErr = sjson.SetBytes(itemBody, "call_id", normalizedCallID)
					if patchErr != nil {
						patchErr = fmt.Errorf("normalize input.%d call_id: %w", currentIndex, patchErr)
						return false
					}
					changed = true
				}
			}
		} else if item.Get("call_id").Exists() {
			itemBody, patchErr = sjson.DeleteBytes(itemBody, "call_id")
			if patchErr != nil {
				patchErr = fmt.Errorf("delete input.%d call_id: %w", currentIndex, patchErr)
				return false
			}
			changed = true
		}

		if codexInputItemRequiresName(itemType) && strings.TrimSpace(item.Get("name").String()) == "" {
			name := strings.TrimSpace(item.Get("tool_name").String())
			if name == "" {
				name = strings.TrimSpace(item.Get("function.name").String())
			}
			if name == "" {
				name = "tool"
			}
			itemBody, patchErr = sjson.SetBytes(itemBody, "name", name)
			if patchErr != nil {
				patchErr = fmt.Errorf("set input.%d tool name: %w", currentIndex, patchErr)
				return false
			}
			changed = true
		}

		id := item.Get("id")
		if id.Type == gjson.String && shouldStripOpenAIResponsesInputItemID(itemType, id.String()) {
			itemBody, patchErr = sjson.DeleteBytes(itemBody, "id")
			if patchErr != nil {
				patchErr = fmt.Errorf("delete input.%d invalid id: %w", currentIndex, patchErr)
				return false
			}
			changed = true
		}
		items = append(items, itemBody)
		return true
	})
	if patchErr != nil {
		return body, patchErr
	}
	if !changed {
		return body, nil
	}

	rebuilt := make([]byte, 0, len(input.Raw))
	rebuilt = append(rebuilt, '[')
	for i, item := range items {
		if i > 0 {
			rebuilt = append(rebuilt, ',')
		}
		rebuilt = append(rebuilt, item...)
	}
	rebuilt = append(rebuilt, ']')
	next, err := sjson.SetRawBytes(body, "input", rebuilt)
	if err != nil {
		return body, fmt.Errorf("replace patched native Codex input: %w", err)
	}
	return next, nil
}

// prepareOpenAICodexUpstreamIdentity applies the official Codex identity
// contract independently from the optional prompt-cache translation feature.
// The request body is the source of truth for session identity, while thread
// identity remains independent so root and child threads can share one cache.
func prepareOpenAICodexUpstreamIdentity(
	c *gin.Context,
	account *Account,
	body []byte,
	compact bool,
) ([]byte, openAICodexUpstreamIdentity, error) {
	if account == nil || account.Type != AccountTypeOAuth {
		return body, openAICodexUpstreamIdentity{}, nil
	}
	identity := resolveOpenAICodexUpstreamIdentity(
		c,
		account,
		body,
		getAPIKeyIDFromContext(c),
		compact,
	)
	updated, err := patchOpenAICodexIdentityBody(
		body,
		account,
		identity,
		openAICodexIdentityBodyOptions{Compact: compact},
	)
	if err != nil {
		return body, openAICodexUpstreamIdentity{}, err
	}
	if c != nil {
		c.Set(openAICodexUpstreamIdentityContextKey, identity)
	}
	return updated, identity, nil
}

func resolveOpenAICodexUpstreamIdentity(
	c *gin.Context,
	account *Account,
	body []byte,
	apiKeyID int64,
	compact bool,
) openAICodexUpstreamIdentity {
	previousIdentity, hasPreviousIdentity := openAICodexUpstreamIdentityFromContext(c)
	rawSessionID := explicitOpenAICodexSessionID(c, body, !compact)
	if rawSessionID == "" && compact {
		rawSessionID = resolveOpenAICompactSessionID(c)
	}

	rawThreadID := ""
	if c != nil && c.Request != nil {
		rawThreadID = strings.TrimSpace(c.Request.Header.Get(openAIOfficialThreadIDHeader))
		if rawThreadID == "" {
			rawThreadID = strings.TrimSpace(c.Request.Header.Get(openAIOfficialClientRequestIDHeader))
		}
	}
	if rawThreadID == "" && !compact {
		rawThreadID = strings.TrimSpace(gjson.GetBytes(body, "client_metadata.thread_id").String())
	}
	bodyTurnMetadata := ""
	if !compact {
		bodyTurnMetadata = strings.TrimSpace(gjson.GetBytes(body, "client_metadata."+openAIWSTurnMetadataHeader).String())
	}
	rawForkedFromThreadID := strings.TrimSpace(gjson.Get(bodyTurnMetadata, "forked_from_thread_id").String())
	if rawForkedFromThreadID == "" && c != nil && c.Request != nil {
		headerTurnMetadata := strings.TrimSpace(c.Request.Header.Get(openAIWSTurnMetadataHeader))
		rawForkedFromThreadID = strings.TrimSpace(gjson.Get(headerTurnMetadata, "forked_from_thread_id").String())
	}
	rawParentThreadID := ""
	if c != nil && c.Request != nil {
		rawParentThreadID = strings.TrimSpace(c.Request.Header.Get(openAICodexParentThreadIDHeader))
	}
	if rawParentThreadID == "" && !compact {
		rawParentThreadID = strings.TrimSpace(gjson.GetBytes(body, "client_metadata."+openAICodexParentThreadIDHeader).String())
		if rawParentThreadID == "" {
			rawParentThreadID = strings.TrimSpace(gjson.Get(bodyTurnMetadata, "parent_thread_id").String())
		}
	}

	identity := openAICodexUpstreamIdentity{
		SessionID:          isolateOpenAISessionID(apiKeyID, rawSessionID),
		ThreadID:           isolateOpenAISessionID(apiKeyID, rawThreadID),
		ForkedFromThreadID: isolateOpenAISessionID(apiKeyID, rawForkedFromThreadID),
		ParentThreadID:     isolateOpenAISessionID(apiKeyID, rawParentThreadID),
	}
	if account != nil {
		identity.InstallationID = strings.TrimSpace(account.GetOpenAIDeviceID())
	}
	// A Responses WebSocket session reuses one gin context across turns. Current
	// Codex clients may omit prompt_cache_key/client_metadata on follow-up
	// response.create frames, so carry forward the already-isolated identity
	// instead of silently dropping the cache/session headers after turn one.
	if hasPreviousIdentity {
		if identity.SessionID == "" {
			identity.SessionID = previousIdentity.SessionID
		}
		if identity.ThreadID == "" {
			identity.ThreadID = previousIdentity.ThreadID
		}
		if identity.ForkedFromThreadID == "" {
			identity.ForkedFromThreadID = previousIdentity.ForkedFromThreadID
		}
		if identity.ParentThreadID == "" {
			identity.ParentThreadID = previousIdentity.ParentThreadID
		}
	}
	if !compact {
		rawWindowID := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.x-codex-window-id").String())
		if rawWindowID == "" && c != nil && c.Request != nil {
			rawWindowID = strings.TrimSpace(c.Request.Header.Get("x-codex-window-id"))
		}
		identity.WindowID = remapNativeCodexWindowID(rawWindowID, rawThreadID, identity.ThreadID)
		if identity.WindowID == "" && hasPreviousIdentity {
			identity.WindowID = previousIdentity.WindowID
		}
	}
	if c != nil && c.Request != nil {
		headerTurnMetadata := strings.TrimSpace(c.Request.Header.Get(openAIWSTurnMetadataHeader))
		if headerTurnMetadata != "" {
			identity.TurnMetadata = rewriteNativeCodexTurnMetadata(headerTurnMetadata, identity)
		}
	}
	return identity
}

func patchOpenAICodexIdentityBody(
	body []byte,
	account *Account,
	identity openAICodexUpstreamIdentity,
	options openAICodexIdentityBodyOptions,
) ([]byte, error) {
	updated := body
	var err error
	if identity.SessionID != "" {
		updated, err = sjson.SetBytes(updated, "prompt_cache_key", identity.SessionID)
		if err != nil {
			return body, fmt.Errorf("isolate Codex prompt_cache_key: %w", err)
		}
	}
	if options.Compact {
		return updated, nil
	}

	clientMetadata := gjson.GetBytes(updated, "client_metadata")
	if clientMetadata.Exists() && !clientMetadata.IsObject() {
		return body, fmt.Errorf("codex client_metadata must be an object")
	}
	patchClientMetadata := clientMetadata.IsObject() || options.CreateClientMetadata
	if !patchClientMetadata {
		return updated, nil
	}
	if identity.SessionID != "" {
		updated, err = sjson.SetBytes(updated, "client_metadata.session_id", identity.SessionID)
		if err != nil {
			return body, fmt.Errorf("isolate Codex client_metadata.session_id: %w", err)
		}
	}
	if identity.ThreadID != "" {
		updated, err = sjson.SetBytes(updated, "client_metadata.thread_id", identity.ThreadID)
		if err != nil {
			return body, fmt.Errorf("isolate Codex client_metadata.thread_id: %w", err)
		}
	}
	if identity.ParentThreadID != "" {
		updated, err = sjson.SetBytes(updated, "client_metadata."+openAICodexParentThreadIDHeader, identity.ParentThreadID)
		if err != nil {
			return body, fmt.Errorf("isolate Codex client_metadata parent thread id: %w", err)
		}
	}
	if identity.WindowID != "" {
		updated, err = sjson.SetBytes(updated, "client_metadata.x-codex-window-id", identity.WindowID)
		if err != nil {
			return body, fmt.Errorf("isolate Codex client metadata window: %w", err)
		}
	}

	deviceID := strings.TrimSpace(identity.InstallationID)
	if deviceID == "" && account != nil {
		deviceID = strings.TrimSpace(account.GetOpenAIDeviceID())
	}
	identity.InstallationID = deviceID
	if deviceID != "" && strings.TrimSpace(gjson.GetBytes(updated, "client_metadata.x-codex-installation-id").String()) != deviceID {
		updated, err = sjson.SetBytes(updated, "client_metadata.x-codex-installation-id", deviceID)
		if err != nil {
			return body, fmt.Errorf("normalize Codex installation id: %w", err)
		}
	}

	bodyTurnMetadata := strings.TrimSpace(gjson.GetBytes(updated, "client_metadata.x-codex-turn-metadata").String())
	if bodyTurnMetadata != "" {
		rewritten := rewriteNativeCodexTurnMetadata(bodyTurnMetadata, identity)
		if rewritten != bodyTurnMetadata {
			updated, err = sjson.SetBytes(updated, "client_metadata.x-codex-turn-metadata", rewritten)
			if err != nil {
				return body, fmt.Errorf("isolate Codex body turn metadata: %w", err)
			}
		}
	}
	return updated, nil
}

func remapNativeCodexWindowID(rawWindowID, rawThreadID, isolatedThreadID string) string {
	rawWindowID = strings.TrimSpace(rawWindowID)
	if rawWindowID == "" || isolatedThreadID == "" {
		return ""
	}
	if rawWindowID == rawThreadID {
		return isolatedThreadID
	}
	if rawThreadID != "" && strings.HasPrefix(rawWindowID, rawThreadID+":") {
		return isolatedThreadID + strings.TrimPrefix(rawWindowID, rawThreadID)
	}
	if separator := strings.LastIndex(rawWindowID, ":"); separator >= 0 {
		return isolatedThreadID + rawWindowID[separator:]
	}
	return isolatedThreadID
}

func rewriteNativeCodexTurnMetadata(raw string, identity openAICodexUpstreamIdentity) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !gjson.Valid(raw) {
		return raw
	}
	updated := raw
	if identity.SessionID != "" && gjson.Get(raw, "prompt_cache_key").Exists() {
		updated, _ = sjson.Set(updated, "prompt_cache_key", identity.SessionID)
	}
	if identity.SessionID != "" && gjson.Get(raw, "session_id").Exists() {
		updated, _ = sjson.Set(updated, "session_id", identity.SessionID)
	}
	if identity.ThreadID != "" && gjson.Get(raw, "thread_id").Exists() {
		updated, _ = sjson.Set(updated, "thread_id", identity.ThreadID)
	}
	if identity.InstallationID != "" && gjson.Get(raw, "installation_id").Exists() {
		updated, _ = sjson.Set(updated, "installation_id", identity.InstallationID)
	}
	if identity.ForkedFromThreadID != "" && gjson.Get(raw, "forked_from_thread_id").Exists() {
		updated, _ = sjson.Set(updated, "forked_from_thread_id", identity.ForkedFromThreadID)
	}
	if identity.ParentThreadID != "" && gjson.Get(raw, "parent_thread_id").Exists() {
		updated, _ = sjson.Set(updated, "parent_thread_id", identity.ParentThreadID)
	}
	if identity.WindowID != "" && gjson.Get(raw, "window_id").Exists() {
		updated, _ = sjson.Set(updated, "window_id", identity.WindowID)
	}
	return updated
}

func openAICodexUpstreamIdentityFromContext(c *gin.Context) (openAICodexUpstreamIdentity, bool) {
	if c == nil {
		return openAICodexUpstreamIdentity{}, false
	}
	value, ok := c.Get(openAICodexUpstreamIdentityContextKey)
	if !ok {
		return openAICodexUpstreamIdentity{}, false
	}
	identity, ok := value.(openAICodexUpstreamIdentity)
	if !ok || identity == (openAICodexUpstreamIdentity{}) {
		return openAICodexUpstreamIdentity{}, false
	}
	return identity, true
}

func applyOpenAICodexUpstreamIdentityHeaders(headers http.Header, identity openAICodexUpstreamIdentity) {
	if headers == nil {
		return
	}
	if identity.InstallationID != "" {
		headers.Set("x-codex-installation-id", identity.InstallationID)
	}
	if identity.SessionID != "" {
		setOpenAIUpstreamSessionHeaders(headers, identity.SessionID)
	}
	if identity.ThreadID != "" {
		setOpenAIUpstreamThreadHeaders(headers, identity.ThreadID)
	}
	if identity.ParentThreadID != "" {
		headers.Set(openAICodexParentThreadIDHeader, identity.ParentThreadID)
	}
	if identity.WindowID != "" {
		headers.Set("x-codex-window-id", identity.WindowID)
	}
	if identity.TurnMetadata != "" {
		headers.Set(openAIWSTurnMetadataHeader, identity.TurnMetadata)
	}
}

func setOpenAIUpstreamThreadHeaders(headers http.Header, value string) {
	if headers == nil {
		return
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	headers.Set(openAIOfficialThreadIDHeader, value)
	headers.Set(openAIOfficialClientRequestIDHeader, value)
}

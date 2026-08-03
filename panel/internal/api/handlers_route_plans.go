package api

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/ladderairport/panel/internal/store"
)

type routePlanRuleBody struct {
	MatchType     string `json:"match_type"`
	MatchValue    string `json:"match_value"`
	Action        string `json:"action"`
	TargetChainID string `json:"target_chain_id"`
	Enabled       *bool  `json:"enabled"`
}

func (s *Server) handleListRoutePlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.Store.ListRoutePlans()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

func (s *Server) handleGetRoutePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.Store.GetRoutePlan(pathID(r))
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

type createRoutePlanBody struct {
	Name           string              `json:"name"`
	Scope          string              `json:"scope"`
	SubscriptionID string              `json:"subscription_id"`
	Enabled        *bool               `json:"enabled"`
	SortOrder      *int                `json:"sort_order"`
	Rules          []routePlanRuleBody `json:"rules"`
}

func (s *Server) handleCreateRoutePlan(w http.ResponseWriter, r *http.Request) {
	var body createRoutePlanBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	plan := &store.RoutePlan{
		Name:           strings.TrimSpace(body.Name),
		Scope:          strings.ToLower(strings.TrimSpace(body.Scope)),
		SubscriptionID: strings.TrimSpace(body.SubscriptionID),
		Enabled:        true,
	}
	if plan.Scope == "" {
		plan.Scope = "global"
	}
	if body.Enabled != nil {
		plan.Enabled = *body.Enabled
	}
	if body.SortOrder != nil {
		plan.SortOrder = *body.SortOrder
	}
	if err := s.validateRoutePlanScalars(plan); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rules, err := s.validateRoutePlanRules(body.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan.Rules = rules
	if err := s.Store.CreateRoutePlan(plan); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, err := s.Store.GetRoutePlan(plan.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reapplyForRoutePlanChange(r, nil, created)
	writeJSON(w, http.StatusCreated, created)
}

type updateRoutePlanBody struct {
	Name      *string             `json:"name"`
	Enabled   *bool               `json:"enabled"`
	SortOrder *int                `json:"sort_order"`
	Rules     []routePlanRuleBody `json:"rules"`
}

func (s *Server) handleUpdateRoutePlan(w http.ResponseWriter, r *http.Request) {
	existing, err := s.Store.GetRoutePlan(pathID(r))
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body updateRoutePlanBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	updated := *existing
	if body.Name != nil {
		updated.Name = strings.TrimSpace(*body.Name)
	}
	if body.Enabled != nil {
		updated.Enabled = *body.Enabled
	}
	if body.SortOrder != nil {
		updated.SortOrder = *body.SortOrder
	}
	// Scope and subscription binding are immutable after creation.
	updated.Scope = existing.Scope
	updated.SubscriptionID = existing.SubscriptionID
	if err := s.validateRoutePlanScalars(&updated); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var rules []store.RoutePlanRule
	if body.Rules != nil {
		rules, err = s.validateRoutePlanRules(body.Rules)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := s.Store.UpdateRoutePlan(&updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.Rules != nil {
		if err := s.Store.ReplaceRoutePlanRules(updated.ID, rules); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	result, err := s.Store.GetRoutePlan(updated.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reapplyForRoutePlanChange(r, existing, result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDeleteRoutePlan(w http.ResponseWriter, r *http.Request) {
	existing, err := s.Store.GetRoutePlan(pathID(r))
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Store.DeleteRoutePlan(existing.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reapplyForRoutePlanChange(r, existing, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// validateRoutePlanScalars checks name/scope/subscription binding.
func (s *Server) validateRoutePlanScalars(plan *store.RoutePlan) error {
	if plan.Name == "" {
		return fmt.Errorf("必须提供名称")
	}
	switch plan.Scope {
	case "global":
		if plan.SubscriptionID != "" {
			return fmt.Errorf("全局路由计划不能绑定订阅")
		}
	case "subscription":
		if plan.SubscriptionID == "" {
			return fmt.Errorf("订阅级路由计划必须提供 subscription_id")
		}
		if _, err := s.Store.GetSubscription(plan.SubscriptionID); err != nil {
			if isNotFound(err) {
				return fmt.Errorf("订阅不存在：%s", plan.SubscriptionID)
			}
			return err
		}
	default:
		return fmt.Errorf("scope 必须是 global 或 subscription")
	}
	return nil
}

// validateRoutePlanRules validates request rules and normalizes them into
// store rules (positions are assigned on insert, following array order).
func (s *Server) validateRoutePlanRules(body []routePlanRuleBody) ([]store.RoutePlanRule, error) {
	rules := make([]store.RoutePlanRule, 0, len(body))
	for i, item := range body {
		rule := store.RoutePlanRule{
			MatchType:  strings.ToLower(strings.TrimSpace(item.MatchType)),
			MatchValue: strings.TrimSpace(item.MatchValue),
			Action:     strings.ToLower(strings.TrimSpace(item.Action)),
			Enabled:    true,
		}
		if item.Enabled != nil {
			rule.Enabled = *item.Enabled
		}
		switch rule.MatchType {
		case "domain", "domain_suffix", "domain_keyword", "process_name":
		case "ip_cidr":
		default:
			return nil, fmt.Errorf("第 %d 条规则的 match_type 无效：%s", i+1, item.MatchType)
		}
		if rule.MatchValue == "" {
			return nil, fmt.Errorf("第 %d 条规则的 match_value 不能为空", i+1)
		}
		if rule.MatchType == "ip_cidr" {
			if _, err := netip.ParsePrefix(rule.MatchValue); err != nil {
				return nil, fmt.Errorf("第 %d 条规则的 ip_cidr 无效：%s", i+1, rule.MatchValue)
			}
		}
		switch rule.Action {
		case "proxy":
			chainID := strings.TrimSpace(item.TargetChainID)
			if chainID == "" {
				return nil, fmt.Errorf("第 %d 条规则（proxy）必须提供 target_chain_id", i+1)
			}
			chain, err := s.Store.GetProxyChain(chainID)
			if err != nil {
				if isNotFound(err) {
					return nil, fmt.Errorf("第 %d 条规则引用的代理链不存在：%s", i+1, chainID)
				}
				return nil, err
			}
			if !chain.Enabled {
				return nil, fmt.Errorf("第 %d 条规则引用的代理链未启用：%s", i+1, chain.Name)
			}
			rule.TargetChainID = chainID
		case "direct", "block":
			rule.TargetChainID = ""
		default:
			return nil, fmt.Errorf("第 %d 条规则的 action 无效：%s", i+1, item.Action)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// reapplyForRoutePlanChange re-pushes node configs when an enabled global
// plan was added/changed/removed (subscription-scope plans only affect
// rendered client configs, never node configs). Best-effort: the apply runs
// as a regular task so failures stay visible under /api/v1/tasks without
// rolling back the plan mutation.
func (s *Server) reapplyForRoutePlanChange(r *http.Request, oldPlan, newPlan *store.RoutePlan) {
	affected := (oldPlan != nil && oldPlan.Scope == "global" && oldPlan.Enabled) ||
		(newPlan != nil && newPlan.Scope == "global" && newPlan.Enabled)
	if !affected || s.Runner == nil {
		return
	}
	nodes, err := s.Store.ListNodes()
	if err != nil || len(nodes) == 0 {
		return
	}
	nodeIDs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		nodeIDs = append(nodeIDs, node.ID)
	}
	task := &store.Task{Type: "apply", Status: "pending", NodeIDs: nodeIDs}
	if err := s.Store.CreateTask(task); err != nil {
		return
	}
	_ = s.Runner.RunTask(r.Context(), task.ID)
}

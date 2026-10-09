package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type CompositeRouteResolver struct {
	repo                   CompositeModelRouteRepository
	modelOwnershipResolver CompositeModelOwnershipResolver
}

func NewCompositeRouteResolver(repo CompositeModelRouteRepository) *CompositeRouteResolver {
	return &CompositeRouteResolver{repo: repo}
}

func (r *CompositeRouteResolver) SetModelOwnershipResolver(resolver CompositeModelOwnershipResolver) {
	if r != nil {
		r.modelOwnershipResolver = resolver
	}
}

// ListExactPublicModels returns enabled, concrete route IDs suitable for a model catalog.
func (r *CompositeRouteResolver) ListExactPublicModels(ctx context.Context, groupID int64, endpoint string, includeSystemOne bool) ([]string, error) {
	if r == nil || r.repo == nil || groupID <= 0 {
		return nil, nil
	}
	routes, err := r.repo.ListByGroup(ctx, groupID, false)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(routes))
	for _, route := range routes {
		if route.Enabled && route.MatchType == CompositeRouteMatchExact &&
			(endpoint == "" || normalizeCompositeRouteEndpoint(route.Endpoint) == CompositeRouteEndpointAny || normalizeCompositeRouteEndpoint(route.Endpoint) == endpoint) {
			if model := strings.TrimSpace(route.PublicModel); model != "" {
				models = append(models, model)
			}
		}
	}
	if !includeSystemOne {
		models = filterSystemOneRouteModels(routes, models, endpoint)
	}
	return models, nil
}

func (r *CompositeRouteResolver) FilterCodexModels(ctx context.Context, groupID int64, models []string) ([]string, error) {
	if r == nil || r.repo == nil || groupID <= 0 {
		return models, nil
	}
	routes, err := r.repo.ListByGroup(ctx, groupID, false)
	if err != nil {
		return nil, err
	}
	return filterSystemOneRouteModels(routes, models, CompositeRouteEndpointResponses), nil
}

func filterSystemOneRouteModels(routes []CompositeModelRoute, models []string, endpoint string) []string {
	filtered := make([]string, 0, len(models))
	for _, model := range models {
		route, matched := matchCompositeRoute(routes, model, normalizeCompositeRouteEndpoint(endpoint))
		if !matched || route.TargetPlatform != PlatformTypeSafe {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func (r *CompositeRouteResolver) Resolve(ctx context.Context, groupID int64, model, endpoint string) (CompositeRouteDecision, error) {
	model = strings.TrimSpace(model)
	endpoint = normalizeCompositeRouteEndpoint(endpoint)
	decision := CompositeRouteDecision{
		GroupID:     groupID,
		PublicModel: model,
		Endpoint:    endpoint,
	}
	if model == "" {
		decision.Reason = "model is required"
		return decision, nil
	}

	if r != nil && r.repo != nil && groupID > 0 {
		routes, err := r.repo.ListByGroup(ctx, groupID, false)
		if err != nil {
			return decision, fmt.Errorf("list composite routes: %w", err)
		}
		if route, ok := matchCompositeRoute(routes, model, endpoint); ok {
			upstreamModel := strings.TrimSpace(route.UpstreamModel)
			if upstreamModel == "" {
				upstreamModel = model
			}
			return CompositeRouteDecision{
				Matched:        true,
				Source:         CompositeRouteSourceExplicit,
				GroupID:        groupID,
				PublicModel:    model,
				TargetPlatform: route.TargetPlatform,
				UpstreamModel:  upstreamModel,
				Endpoint:       endpoint,
				Route:          &route,
			}, nil
		}
	}

	if r != nil && r.modelOwnershipResolver != nil && groupID > 0 {
		ownership, err := r.modelOwnershipResolver(ctx, groupID, model)
		if err != nil {
			// A recognizable model can still use the existing detector when the
			// account catalog is temporarily unavailable. Unknown aliases cannot.
			if _, detectable := DetectModelPlatform(model); !detectable {
				return decision, fmt.Errorf("resolve account model ownership: %w", err)
			}
		} else if ownership.Ambiguous {
			// Recognizable text models have a canonical primary. Other owners
			// are considered only by the bounded cross-provider failover plan.
			if platform, ok := DetectModelPlatform(model); ok && compositeTextEndpoint(endpoint) {
				source := CompositeRouteSourceDetector
				// If all native accounts were disabled/removed, start with a
				// configured compatible owner instead of a terminal model-not-found.
				owners := orderedCompositeOwners(ownership.Platforms)
				if len(owners) > 0 {
					nativeOwned := false
					for _, owner := range owners {
						if owner == platform {
							nativeOwned = true
							break
						}
					}
					if !nativeOwned {
						platform = owners[0]
						source = CompositeRouteSourceAccount
					}
				}
				return CompositeRouteDecision{Matched: true, Source: source,
					GroupID: groupID, PublicModel: model, TargetPlatform: platform,
					UpstreamModel: model, Endpoint: endpoint}, nil
			}
			decision.Reason = "model is exposed by multiple provider platforms"
			return decision, nil
		} else if ownership.Matched {
			platform := strings.TrimSpace(ownership.TargetPlatform)
			if !isConcreteRequestPlatform(platform) {
				decision.Reason = "account model ownership has no concrete target platform"
				return decision, nil
			}
			return CompositeRouteDecision{
				Matched:        true,
				Source:         CompositeRouteSourceAccount,
				GroupID:        groupID,
				PublicModel:    model,
				TargetPlatform: platform,
				UpstreamModel:  model,
				Endpoint:       endpoint,
			}, nil
		}
	}

	if platform, ok := DetectModelPlatform(model); ok {
		return CompositeRouteDecision{
			Matched:        true,
			Source:         CompositeRouteSourceDetector,
			GroupID:        groupID,
			PublicModel:    model,
			TargetPlatform: platform,
			UpstreamModel:  model,
			Endpoint:       endpoint,
		}, nil
	}
	decision.Reason = "no explicit route or built-in detector match"
	return decision, nil
}

func matchCompositeRoute(routes []CompositeModelRoute, model, endpoint string) (CompositeModelRoute, bool) {
	if len(routes) == 0 {
		return CompositeModelRoute{}, false
	}

	type candidate struct {
		route          CompositeModelRoute
		matchStrength  int
		endpointWeight int
		prefixLen      int
	}
	candidates := make([]candidate, 0, len(routes))
	for _, route := range routes {
		route.Endpoint = normalizeCompositeRouteEndpoint(route.Endpoint)
		if route.Endpoint != endpoint && route.Endpoint != CompositeRouteEndpointAny {
			continue
		}
		route.MatchType = normalizeCompositeRouteMatchType(route.MatchType)
		publicModel := strings.TrimSpace(route.PublicModel)
		if publicModel == "" {
			continue
		}

		matchStrength := 0
		prefixLen := len(publicModel)
		switch route.MatchType {
		case CompositeRouteMatchExact:
			if publicModel != model {
				continue
			}
			matchStrength = 2
		case CompositeRouteMatchPrefix:
			if !strings.HasPrefix(model, publicModel) {
				continue
			}
			matchStrength = 1
		default:
			continue
		}
		endpointWeight := 0
		if route.Endpoint == endpoint {
			endpointWeight = 1
		}
		candidates = append(candidates, candidate{
			route:          route,
			matchStrength:  matchStrength,
			endpointWeight: endpointWeight,
			prefixLen:      prefixLen,
		})
	}
	if len(candidates) == 0 {
		return CompositeModelRoute{}, false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.matchStrength != b.matchStrength {
			return a.matchStrength > b.matchStrength
		}
		if a.endpointWeight != b.endpointWeight {
			return a.endpointWeight > b.endpointWeight
		}
		if a.prefixLen != b.prefixLen {
			return a.prefixLen > b.prefixLen
		}
		if a.route.Priority != b.route.Priority {
			return a.route.Priority < b.route.Priority
		}
		return a.route.ID < b.route.ID
	})
	return candidates[0].route, true
}

// ResolveCandidates limits automatic failover to exact model owners in this
// group. Explicit routes remain pinned; unknown aliases remain fail-closed.
func (r *CompositeRouteResolver) ResolveCandidates(ctx context.Context, groupID int64, model, endpoint string) ([]CompositeRouteDecision, error) {
	primary, err := r.Resolve(ctx, groupID, model, endpoint)
	if err != nil || !primary.Matched {
		return nil, err
	}
	decisions := []CompositeRouteDecision{primary}
	if primary.Source == CompositeRouteSourceExplicit || !compositeTextEndpoint(endpoint) || r == nil || r.modelOwnershipResolver == nil {
		return decisions, nil
	}
	if _, ok := DetectModelPlatform(model); !ok {
		return decisions, nil
	}
	ownership, err := r.modelOwnershipResolver(ctx, groupID, strings.TrimSpace(model))
	if err != nil {
		return decisions, nil
	} // preserve canonical-provider availability on catalog errors
	platforms := orderedCompositeOwners(ownership.Platforms)
	seen := map[string]bool{primary.TargetPlatform: true}
	for _, platform := range platforms {
		if seen[platform] || !compositeTextPlatform(platform) {
			continue
		}
		seen[platform] = true
		candidate := primary
		candidate.TargetPlatform = platform
		candidate.Source = CompositeRouteSourceAccount
		decisions = append(decisions, candidate)
	}
	return decisions, nil
}

func compositeTextEndpoint(endpoint string) bool {
	switch endpoint {
	case CompositeRouteEndpointMessages, CompositeRouteEndpointChatCompletions, CompositeRouteEndpointResponses:
		return true
	default:
		return false
	}
}

func compositeTextPlatform(platform string) bool {
	switch platform {
	case PlatformAnthropic, PlatformOpenAI, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo:
		return true
	default:
		return false
	}
}

func orderedCompositeOwners(owners []string) []string {
	platforms := make([]string, 0, len(owners))
	for _, platform := range owners {
		if compositeTextPlatform(platform) {
			platforms = append(platforms, platform)
		}
	}
	sort.Slice(platforms, func(i, j int) bool {
		if platforms[i] == PlatformOpenCodeGo {
			return platforms[j] != PlatformOpenCodeGo
		}
		if platforms[j] == PlatformOpenCodeGo {
			return false
		}
		return platforms[i] < platforms[j]
	})
	return platforms
}

package terraform

import (
	"fmt"

	"github.com/hashicorp/terraform/addrs"
	"github.com/zclconf/go-cty/cty"
)

// NodePlannableResourceInstance represents a _single_ resource
// instance that is plannable. This means this represents a single
// count index, for example.
type NodePlannableResourceInstance struct {
	*NodeAbstractResourceInstance
}

var (
	_ GraphNodeSubPath              = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeReferenceable        = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeReferencer           = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeResource             = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeResourceInstance     = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeAttachResourceConfig = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeAttachResourceState  = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeEvalable             = (*NodePlannableResourceInstance)(nil)
)

// GraphNodeEvalable
func (n *NodePlannableResourceInstance) EvalTree() EvalNode {
	addr := n.ResourceInstanceAddr()

	// State still uses legacy-style internal ids, so we need to shim to get
	// a suitable key to use.
	stateId := NewLegacyResourceInstanceAddress(addr).stateId()

	// Build the instance info. More of this will be populated during eval
	info := NewInstanceInfo(addr.ContainingResource())

	// Determine the dependencies for the state.
	stateDeps := n.StateReferences()

	// Eval info is different depending on what kind of resource this is
	switch addr.Resource.Resource.Mode {
	case addrs.ManagedResourceMode:
		return n.evalTreeManagedResource(addr, stateId, info, stateDeps)
	case addrs.DataResourceMode:
		return n.evalTreeDataResource(addr, stateId, info, stateDeps)
	default:
		panic(fmt.Errorf("unsupported resource mode %s", n.Config.Mode))
	}
}

func (n *NodePlannableResourceInstance) evalTreeDataResource(addr addrs.AbsResourceInstance, stateId string, info *InstanceInfo, stateDeps []string) EvalNode {
	var provider ResourceProvider
	var providerSchema *ProviderSchema
	var diff *InstanceDiff
	var state *InstanceState
	var configVal cty.Value

	return &EvalSequence{
		Nodes: []EvalNode{
			&EvalReadState{
				Name:   stateId,
				Output: &state,
			},

			&EvalGetProvider{
				Addr:   n.ResolvedProvider.ProviderConfig,
				Output: &provider,
			},

			&EvalReadDataDiff{
				Addr:           addr.Resource,
				Config:         n.Config,
				Provider:       &provider,
				ProviderSchema: &providerSchema,
				Output:         &diff,
				OutputValue:    &configVal,
				OutputState:    &state,
			},

			&EvalIf{
				If: func(ctx EvalContext) (bool, error) {
					computed := !configVal.IsWhollyKnown()

					// If the configuration is complete and we
					// already have a state then we don't need to
					// do any further work during apply, because we
					// already populated the state during refresh.
					if !computed && state != nil {
						return true, EvalEarlyExitError{}
					}

					return true, nil
				},
				Then: EvalNoop{},
			},

			&EvalWriteState{
				Name:         stateId,
				ResourceType: n.Config.Type,
				Provider:     n.ResolvedProvider,
				Dependencies: stateDeps,
				State:        &state,
			},

			&EvalWriteDiff{
				Name: stateId,
				Diff: &diff,
			},
		},
	}
}

func (n *NodePlannableResourceInstance) evalTreeManagedResource(addr addrs.AbsResourceInstance, stateId string, info *InstanceInfo, stateDeps []string) EvalNode {
	// Declare a bunch of variables that are used for state during
	// evaluation. Most of this are written to by-address below.
	var provider ResourceProvider
	var diff *InstanceDiff
	var state *InstanceState
	var resourceConfig *ResourceConfig

	return &EvalSequence{
		Nodes: []EvalNode{
			&EvalInterpolate{
				Config:   n.Config.RawConfig.Copy(),
				Resource: resource,
				Output:   &resourceConfig,
			},
			&EvalGetProvider{
				Name:   n.ResolvedProvider,
				Output: &provider,
			},
			// Re-run validation to catch any errors we missed, e.g. type
			// mismatches on computed values.
			&EvalValidateResource{
				Provider:       &provider,
				Config:         &resourceConfig,
				ResourceName:   n.Config.Name,
				ResourceType:   n.Config.Type,
				ResourceMode:   n.Config.Mode,
				IgnoreWarnings: true,
			},
			&EvalReadState{
				Name:   stateId,
				Output: &state,
			},
			&EvalDiff{
				Name:        stateId,
				Info:        info,
				Config:      &resourceConfig,
				Resource:    n.Config,
				Provider:    &provider,
				State:       &state,
				OutputDiff:  &diff,
				OutputState: &state,
			},
			&EvalCheckPreventDestroy{
				Resource: n.Config,
				Diff:     &diff,
			},
			&EvalWriteState{
				Name:         stateId,
				ResourceType: n.Config.Type,
				Provider:     n.ResolvedProvider,
				Dependencies: stateDeps,
				State:        &state,
			},
			&EvalWriteDiff{
				Name: stateId,
				Diff: &diff,
			},
		},
	}
}

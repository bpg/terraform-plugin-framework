// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package basetypes

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var (
	_ SetTypable             = SetType{}
	_ xattr.TypeWithValidate = SetType{}
)

// SetTypable extends attr.Type for set types.
// Implement this interface to create a custom SetType type.
type SetTypable interface {
	attr.Type

	// ValueFromSet should convert the Set to a SetValuable type.
	ValueFromSet(context.Context, SetValue) (SetValuable, diag.Diagnostics)
}

// SetType is an AttributeType representing a set of values. All values must
// be of the same type, which the provider must specify as the ElemType
// property.
type SetType struct {
	ElemType attr.Type
}

// ElementType returns the attr.Type elements will be created from.
func (st SetType) ElementType() attr.Type {
	return st.ElemType
}

// WithElementType returns a SetType that is identical to `l`, but with the
// element type set to `typ`.
func (st SetType) WithElementType(typ attr.Type) attr.TypeWithElementType {
	return SetType{ElemType: typ}
}

// TerraformType returns the tftypes.Type that should be used to
// represent this type. This constrains what user input will be
// accepted and what kind of data can be set in state. The framework
// will use this to translate the AttributeType to something Terraform
// can understand.
func (st SetType) TerraformType(ctx context.Context) tftypes.Type {
	return tftypes.Set{
		ElementType: st.ElemType.TerraformType(ctx),
	}
}

// ValueFromTerraform returns an attr.Value given a tftypes.Value.
// This is meant to convert the tftypes.Value into a more convenient Go
// type for the provider to consume the data with.
func (st SetType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	if in.Type() == nil {
		return NewSetNull(st.ElemType), nil
	}
	if !in.Type().Equal(st.TerraformType(ctx)) {
		return nil, fmt.Errorf("can't use %s as value of Set with ElementType %T, can only use %s values", in.String(), st.ElemType, st.ElemType.TerraformType(ctx).String())
	}
	if !in.IsKnown() {
		return NewSetUnknown(st.ElemType), nil
	}
	if in.IsNull() {
		return NewSetNull(st.ElemType), nil
	}
	val := []tftypes.Value{}
	err := in.As(&val)
	if err != nil {
		return nil, err
	}
	elems := make([]attr.Value, 0, len(val))
	for _, elem := range val {
		av, err := st.ElemType.ValueFromTerraform(ctx, elem)
		if err != nil {
			return nil, err
		}
		elems = append(elems, av)
	}
	// ValueFromTerraform above on each element should make this safe.
	// Otherwise, this will need to do some Diagnostics to error conversion.
	return NewSetValueMust(st.ElemType, elems), nil
}

// Equal returns true if `o` is also a SetType and has the same ElemType.
func (st SetType) Equal(o attr.Type) bool {
	if st.ElemType == nil {
		return false
	}
	other, ok := o.(SetType)
	if !ok {
		return false
	}
	return st.ElemType.Equal(other.ElemType)
}

// ApplyTerraform5AttributePathStep applies the given AttributePathStep to the
// set.
func (st SetType) ApplyTerraform5AttributePathStep(step tftypes.AttributePathStep) (interface{}, error) {
	if _, ok := step.(tftypes.ElementKeyValue); !ok {
		return nil, fmt.Errorf("cannot apply step %T to SetType", step)
	}

	return st.ElemType, nil
}

// String returns a human-friendly description of the SetType.
func (st SetType) String() string {
	return "types.SetType[" + st.ElemType.String() + "]"
}

// Validate implements type validation. This type requires all elements to be
// unique.
func (st SetType) Validate(ctx context.Context, in tftypes.Value, path path.Path) diag.Diagnostics {
	var diags diag.Diagnostics

	if in.Type() == nil {
		return diags
	}

	if !in.Type().Is(tftypes.Set{}) {
		err := fmt.Errorf("expected Set value, received %T with value: %v", in, in)
		diags.AddAttributeError(
			path,
			"Set Type Validation Error",
			"An unexpected error was encountered trying to validate an attribute value. This is always an error in the provider. Please report the following to the provider developer:\n\n"+err.Error(),
		)
		return diags
	}

	if !in.IsKnown() || in.IsNull() {
		return diags
	}

	var elems []tftypes.Value

	if err := in.As(&elems); err != nil {
		diags.AddAttributeError(
			path,
			"Set Type Validation Error",
			"An unexpected error was encountered trying to validate an attribute value. This is always an error in the provider. Please report the following to the provider developer:\n\n"+err.Error(),
		)
		return diags
	}

	validatableType, isValidatable := st.ElemType.(xattr.TypeWithValidate)

	// Attempting to use map[tftypes.Value]struct{} for duplicate detection yields:
	//   panic: runtime error: hash of unhashable type tftypes.primitive
	// Instead, use for loops.
	for indexOuter, elemOuter := range elems {
		// Only evaluate fully known values for duplicates and validation.
		if !elemOuter.IsFullyKnown() {
			continue
		}

		// Validate the element first
		if isValidatable {
			elemValue, err := st.ElemType.ValueFromTerraform(ctx, elemOuter)
			if err != nil {
				diags.AddAttributeError(
					path,
					"Set Type Validation Error",
					"An unexpected error was encountered trying to validate an attribute value. This is always an error in the provider. Please report the following to the provider developer:\n\n"+err.Error(),
				)
				return diags
			}
			diags = append(diags, validatableType.Validate(ctx, elemOuter, path.AtSetValue(elemValue))...)
		}

		// Then check for duplicates
		for indexInner := indexOuter + 1; indexInner < len(elems); indexInner++ {
			elemInner := elems[indexInner]

			if !elemInner.Equal(elemOuter) {
				continue
			}

			// TODO: Point at element attr.Value when Validate method is converted to attr.Value
			// Reference: https://github.com/hashicorp/terraform-plugin-framework/issues/172
			diags.AddAttributeError(
				path,
				"Duplicate Set Element",
				fmt.Sprintf("This attribute contains duplicate values of: %s", elemInner),
			)
		}
	}

	return diags
}

// ValueType returns the Value type.
func (st SetType) ValueType(_ context.Context) attr.Value {
	return SetValue{
		elementType: st.ElemType,
	}
}

// ValueFromSet returns a SetValuable type given a Set.
func (st SetType) ValueFromSet(_ context.Context, set SetValue) (SetValuable, diag.Diagnostics) {
	return set, nil
}

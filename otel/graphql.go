package otel

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// GraphQLConfig holds configuration for GraphQL tracing
type GraphQLConfig struct {
	// ServiceName is used as part of the tracer name
	ServiceName string

	// DisableFieldTracing disables creating spans for individual field resolvers
	DisableFieldTracing bool

	// TracerProvider allows using a custom tracer provider instead of the global one
	TracerProvider trace.TracerProvider
}

// NewGraphQLMiddleware creates a new gqlgen extension for OpenTelemetry tracing
func NewGraphQLMiddleware(cfg *GraphQLConfig) *GraphQLExtension {
	tp := cfg.TracerProvider
	if tp == nil {
		tp = otel.GetTracerProvider()
	}

	return &GraphQLExtension{
		tracer:             tp.Tracer(fmt.Sprintf("%s/graphql", cfg.ServiceName)),
		disableFieldTraces: cfg.DisableFieldTracing,
	}
}

// GraphQLExtension implements the gqlgen extension interface for OpenTelemetry tracing
type GraphQLExtension struct {
	tracer             trace.Tracer
	disableFieldTraces bool
}

func (e *GraphQLExtension) ExtensionName() string {
	return "OpenTelemetry"
}

func (e *GraphQLExtension) Validate(_ graphql.ExecutableSchema) error {
	return nil
}

func (e *GraphQLExtension) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	if !graphql.HasOperationContext(ctx) {
		return next(ctx)
	}

	oc := graphql.GetOperationContext(ctx)

	// Skip tracing for introspection queries
	if isIntrospectionQuery(oc) {
		return next(ctx)
	}

	opName := oc.OperationName
	if opName == "" {
		opName = "unnamed"
	}

	opType := "query"
	if oc.Operation != nil {
		opType = string(oc.Operation.Operation)
	}

	ctx, span := e.tracer.Start(ctx, fmt.Sprintf("GraphQL %s", opType),
		trace.WithAttributes(
			attribute.String("graphql.operation.name", opName),
			attribute.String("graphql.operation.type", opType),
			attribute.Int("graphql.operation.selected_fields", len(oc.Operation.SelectionSet)),
		),
	)
	defer span.End()

	// Return a response handler that will handle errors
	rh := next(ctx)
	return func(ctx context.Context) *graphql.Response {
		resp := rh(ctx)

		// Add any errors to the span
		if len(resp.Errors) > 0 {
			span.SetStatus(codes.Error, resp.Errors[0].Error())
			for _, err := range resp.Errors {
				span.RecordError(err)
				span.SetAttributes(
					attribute.String("error.type", fmt.Sprintf("%T", err)),
					attribute.String("error.message", err.Error()),
				)
			}
		} else {
			span.SetStatus(codes.Ok, "")
		}

		return resp
	}
}

func (e *GraphQLExtension) InterceptField(ctx context.Context, next graphql.Resolver) (interface{}, error) {
	if e.disableFieldTraces {
		return next(ctx)
	}

	fc := graphql.GetFieldContext(ctx)
	if fc == nil {
		return next(ctx)
	}

	// Skip tracing for introspection fields and non-resolver fields
	if !fc.IsResolver || isIntrospectionField(fc) {
		return next(ctx)
	}

	// Get operation context for variables
	oc := graphql.GetOperationContext(ctx)
	if oc == nil {
		return next(ctx)
	}

	// Get loader info from context
	loaderName := ""
	isStopTime := false
	if loader := fc.Object; loader != "" {
		// The object name often matches the loader name pattern
		if strings.HasSuffix(loader, "ByID") || strings.HasSuffix(loader, "ByIDs") {
			loaderName = loader
			if strings.Contains(loader, "StopTime") {
				isStopTime = true
			}
		}
	}

	// Create span for field resolution
	ctx, span := e.tracer.Start(ctx, fmt.Sprintf("Resolve %s", fc.Field.Name),
		trace.WithAttributes(
			// Standard field attributes
			attribute.String("graphql.field.name", fc.Field.Name),
			attribute.String("graphql.field.path", fc.Path().String()),
			attribute.String("graphql.field.type", fc.Field.Definition.Type.String()),
			attribute.String("graphql.field.object", fc.Object),
			attribute.String("graphql.field.args", formatFieldArgs(fc.Field.ArgumentMap(oc.Variables))),
			// Dataloader-specific attributes
			attribute.String("graphql.field.loader", loaderName),
			attribute.Bool("graphql.field.is_list", strings.HasPrefix(fc.Field.Definition.Type.String(), "[")),

			attribute.Bool("graphql.field.is_stop_time", isStopTime),
		),
	)
	defer span.End()

	// Add alias if one is used
	if fc.Field.Alias != "" && fc.Field.Alias != fc.Field.Name {
		span.SetAttributes(attribute.String("graphql.field.alias", fc.Field.Alias))
	}

	// Execute resolver and capture any error
	result, err := next(ctx)
	if err != nil {
		// Record error details
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		span.SetAttributes(
			attribute.String("error.type", fmt.Sprintf("%T", err)),
			attribute.String("error.message", err.Error()),
		)
		// Add error to the GraphQL response
		graphql.AddError(ctx, err)
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return result, err
}

// isIntrospectionQuery checks if the operation is an introspection query
func isIntrospectionQuery(oc *graphql.OperationContext) bool {
	if oc.Operation == nil || oc.Operation.SelectionSet == nil {
		return false
	}
	for _, selection := range oc.Operation.SelectionSet {
		if field, ok := selection.(*ast.Field); ok {
			if field.Name == "__schema" || field.Name == "__type" {
				return true
			}
		}
	}
	return false
}

// isIntrospectionField checks if the field is an introspection field
func isIntrospectionField(fc *graphql.FieldContext) bool {
	return len(fc.Field.Name) > 2 && fc.Field.Name[:2] == "__"
}

// formatFieldArgs formats field arguments into a string representation
// This is useful for tracing but avoids including potentially sensitive values
func formatFieldArgs(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}
	var keys []string
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

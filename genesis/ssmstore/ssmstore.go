// Package ssmstore is a genesis.Store on AWS Systems Manager Parameter Store:
// each credential is a SecureString parameter, read with decryption.
package ssmstore

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// API is the part of the SSM client the store calls.
type API interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	PutParameter(ctx context.Context, in *ssm.PutParameterInput, opts ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// Store keeps credentials as SecureString parameters, the key being the
// parameter's full name.
type Store struct{ Client API }

// Get reads a parameter with decryption; a missing parameter is an error
// (genesis treats any error as "not stored").
func (s Store) Get(ctx context.Context, key string) (string, error) {
	out, err := s.Client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(key),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("get SSM parameter %s: %w", key, err)
	}

	if out.Parameter == nil {
		return "", nil
	}

	return aws.ToString(out.Parameter.Value), nil
}

// Put creates or overwrites a SecureString parameter.
func (s Store) Put(ctx context.Context, key, value string) error {
	if _, err := s.Client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(key),
		Value:     aws.String(value),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	}); err != nil {
		return fmt.Errorf("put SSM parameter %s: %w", key, err)
	}

	return nil
}

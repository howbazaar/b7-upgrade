// Copyright 2013 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package b7

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/juju/names.v2"
)

const ServiceTagKind = "service"

const (
	ServiceSnippet = "(?:[a-z][a-z0-9]*(?:-[a-z0-9]*[a-z][a-z0-9]*)*)"
	NumberSnippet  = "(?:0|[1-9][0-9]*)"
)

var validService = regexp.MustCompile("^" + ServiceSnippet + "$")

// IsValidService returns whether name is a valid service name.
func IsValidService(name string) bool {
	return validService.MatchString(name)
}

type ServiceTag struct {
	Name string
}

func (t ServiceTag) String() string { return t.Kind() + "-" + t.Id() }
func (t ServiceTag) Kind() string   { return ServiceTagKind }
func (t ServiceTag) Id() string     { return t.Name }

// NewServiceTag returns the tag for the service with the given name.
func NewServiceTag(serviceName string) ServiceTag {
	return ServiceTag{Name: serviceName}
}

// ParseServiceTag parses a service tag string.
func ParseServiceTag(serviceTag string) (ServiceTag, error) {
	tag, err := ParseTag(serviceTag)
	if err != nil {
		return ServiceTag{}, err
	}
	st, ok := tag.(ServiceTag)
	if !ok {
		return ServiceTag{}, invalidTagError(serviceTag, ServiceTagKind)
	}
	return st, nil
}

// TagKind returns one of the *TagKind constants for the given tag, or
// an error if none matches.
func TagKind(tag string) (string, error) {
	i := strings.Index(tag, "-")
	if i <= 0 || !validKinds(tag[:i]) {
		return "", fmt.Errorf("%q is not a valid tag", tag)
	}
	return tag[:i], nil
}

func validKinds(kind string) bool {
	switch kind {
	case ServiceTagKind:
		return true
	}
	return false
}

func splitTag(tag string) (string, string, error) {
	kind, err := TagKind(tag)
	if err != nil {
		return "", "", err
	}
	return kind, tag[len(kind)+1:], nil
}

// ParseTag parses a string representation into a Tag.
func ParseTag(tag string) (names.Tag, error) {
	kind, id, err := splitTag(tag)
	if err != nil {
		return nil, invalidTagError(tag, "")
	}
	switch kind {
	case ServiceTagKind:
		if !IsValidService(id) {
			return nil, invalidTagError(tag, kind)
		}
		return NewServiceTag(id), nil
	default:
		return nil, invalidTagError(tag, "")
	}
}

func invalidTagError(tag, kind string) error {
	if kind != "" {
		return fmt.Errorf("%q is not a valid %s tag", tag, kind)
	}
	return fmt.Errorf("%q is not a valid tag", tag)
}

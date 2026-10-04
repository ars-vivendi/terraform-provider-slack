package v1beta1

import (
	"reflect"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

const Group = "slack.m.arsvivendi.io"
const Version = "v1beta1"

var (
	SchemeGroupVersion                      = schema.GroupVersion{Group: Group, Version: Version}
	SchemeBuilder                           = &scheme.Builder{GroupVersion: SchemeGroupVersion}
	AddToScheme                             = SchemeBuilder.AddToScheme
	ProviderConfigKind                      = reflect.TypeOf(ProviderConfig{}).Name()
	ProviderConfigGroupKind                 = schema.GroupKind{Group: Group, Kind: ProviderConfigKind}.String()
	ProviderConfigGroupVersionKind          = SchemeGroupVersion.WithKind(ProviderConfigKind)
	ClusterProviderConfigKind               = reflect.TypeOf(ClusterProviderConfig{}).Name()
	ClusterProviderConfigGroupKind          = schema.GroupKind{Group: Group, Kind: ClusterProviderConfigKind}.String()
	ClusterProviderConfigGroupVersionKind   = SchemeGroupVersion.WithKind(ClusterProviderConfigKind)
	ProviderConfigUsageGroupVersionKind     = SchemeGroupVersion.WithKind("ProviderConfigUsage")
	ProviderConfigUsageListGroupVersionKind = SchemeGroupVersion.WithKind("ProviderConfigUsageList")
)

func init() {
	SchemeBuilder.Register(&ProviderConfig{}, &ProviderConfigList{}, &ClusterProviderConfig{}, &ClusterProviderConfigList{}, &ProviderConfigUsage{}, &ProviderConfigUsageList{})
}

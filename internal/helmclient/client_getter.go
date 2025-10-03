package helmclient

import (
	"os"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/disk"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// NewRESTClientGetter returns a RESTClientGetter using the provided 'namespace', 'kubeConfig' and 'restConfig'.
//
// source: https://github.com/helm/helm/issues/6910#issuecomment-601277026
func NewRESTClientGetter(namespace string, kubeConfig []byte, restConfig *rest.Config, opts ...RESTClientOption) *RESTClientGetter {
	return &RESTClientGetter{
		namespace:  namespace,
		kubeConfig: kubeConfig,
		restConfig: restConfig,
		opts:       opts,
	}
}

// ToRESTConfig returns a REST config build from a given kubeconfig
func (c *RESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	if c.restConfig != nil {
		return c.restConfig, nil
	}

	return clientcmd.RESTConfigFromKubeConfig(c.kubeConfig)

}

// ToDiscoveryClient returns a CachedDiscoveryInterface that can be used as a discovery client.
func (c *RESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	config, err := c.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	// The more API groups exist, the more discovery requests need to be made.
	// Given 25 API groups with about one version each, discovery needs to make 50 requests.
	// This setting is only used for discovery.
	config.Burst = 100

	for _, fn := range c.opts {
		fn(config)
	}

	discoveryClient, _ := discovery.NewDiscoveryClientForConfig(config)
	return memory.NewMemCacheClient(discoveryClient), nil
}

func (c *RESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := c.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	expander := restmapper.NewShortcutExpander(mapper, discoveryClient, nil)
	return expander, nil
}

func (c *RESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	// use the standard defaults for this client command
	// DEPRECATED: remove and replace with something more accurate
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig

	overrides := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}
	overrides.Context.Namespace = c.namespace

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
}

type CachedClients struct {
	discoveryClient discovery.CachedDiscoveryInterface
	_RESTMapper     meta.RESTMapper
}

func NewCachedClients(cfg *rest.Config) (CachedClients, error) {
	dir, err := os.MkdirTemp("", "helmclient")
	if err != nil {
		return CachedClients{}, err
	}
	cachedDiscovery, err := disk.NewCachedDiscoveryClientForConfig(cfg, dir, "", 0)
	if err != nil {
		return CachedClients{}, err
	}

	cachedDiscovery.Invalidate()

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)
	mapper.Reset()
	expander := restmapper.NewShortcutExpander(mapper, cachedDiscovery, nil)

	return CachedClients{
		discoveryClient: cachedDiscovery,
		_RESTMapper:     expander,
	}, nil
}

var _ clientcmd.ClientConfig = &namespaceClientConfig{}

type namespaceClientConfig struct {
	namespace string
}

func (c namespaceClientConfig) RawConfig() (clientcmdapi.Config, error) {
	return clientcmdapi.Config{}, nil
}

func (c namespaceClientConfig) ClientConfig() (*rest.Config, error) {
	return nil, nil
}

func (c namespaceClientConfig) Namespace() (string, bool, error) {
	return c.namespace, false, nil
}

func (c namespaceClientConfig) ConfigAccess() clientcmd.ConfigAccess {
	return nil
}

// NewCachedRESTClientGetter returns a RESTClientGetter using the provided 'namespace', 'kubeConfig', and 'restConfig',
// and uses cached clients for discovery and REST mapping to improve performance and reduce API server load.
func NewCachedRESTClientGetter(namespace string, kubeConfig []byte, restConfig *rest.Config, clients *CachedClients, opts ...RESTClientOption) *CachedRESTClientGetter {
	return &CachedRESTClientGetter{
		kubeConfig:      kubeConfig,
		restConfig:      restConfig,
		discoveryClient: clients.discoveryClient,
		restMapper:      clients._RESTMapper,
		namespaceConfig: &namespaceClientConfig{namespace: namespace},
		opts:            opts,
	}
}

// ToRESTConfig returns a REST config build from a given kubeconfig
func (c *CachedRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	if c.restConfig != nil {
		return c.restConfig, nil
	}

	return clientcmd.RESTConfigFromKubeConfig(c.kubeConfig)

}

// ToDiscoveryClient returns a CachedDiscoveryInterface that can be used as a discovery client.
func (c *CachedRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return c.discoveryClient, nil
}

func (c *CachedRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	return c.restMapper, nil
}

func (c *CachedRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return c.namespaceConfig
}

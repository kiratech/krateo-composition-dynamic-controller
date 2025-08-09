package chartinspector

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Resource struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type Parameters struct {
	CompositionName                string `json:"compositionName"`                // The name of the composition. Required.
	CompositionNamespace           string `json:"compositionNamespace"`           // The namespace of the composition. Required.
	CompositionGroup               string `json:"compositionGroup"`               // The group of the composition. Optional, defaults to "composition.krateo.io"
	CompositionVersion             string `json:"compositionVersion"`             // The version of the composition. Required.
	CompositionResource            string `json:"compositionResource"`            // The resource name of the composition. Required.
	CompositionDefinitionName      string `json:"compositionDefinitionName"`      // The name of the composition definition. Required.
	CompositionDefinitionNamespace string `json:"compositionDefinitionNamespace"` // The namespace of the composition definition. Required.
	CompositionDefinitionGroup     string `json:"compositionDefinitionGroup"`     // The group of the composition definition. Optional, defaults to "core.krateo.io"
	CompositionDefinitionVersion   string `json:"compositionDefinitionVersion"`   // The version of the composition definition. Optional, defaults to "v1alpha1"
	CompositionDefinitionResource  string `json:"compositionDefinitionResource"`  // The resource name of the composition definition. Optional, defaults to "compositiondefinitions"
}

type ChartInspectorInterface interface {
	Resources(Parameters) ([]Resource, error)
}

type ChartInspector struct {
	server     string
	httpClient *http.Client
}

var _ ChartInspectorInterface = &ChartInspector{}

func NewChartInspector(server string) ChartInspector {
	return ChartInspector{server: server, httpClient: http.DefaultClient}
}

func (c *ChartInspector) WithHTTPClient(httpClient *http.Client) {
	c.httpClient = httpClient
}

func (c *ChartInspector) WithServer(server string) {
	c.server = server
}

func (c *ChartInspector) Validate(params Parameters) error {
	if params.CompositionName == "" {
		return fmt.Errorf("compositionName is required")
	}
	if params.CompositionNamespace == "" {
		return fmt.Errorf("compositionNamespace is required")
	}
	if params.CompositionVersion == "" {
		return fmt.Errorf("compositionVersion is required")
	}
	if params.CompositionResource == "" {
		return fmt.Errorf("compositionResource is required")
	}
	if params.CompositionDefinitionName == "" {
		return fmt.Errorf("compositionDefinitionName is required")
	}
	if params.CompositionDefinitionNamespace == "" {
		return fmt.Errorf("compositionDefinitionNamespace is required")
	}
	return nil
}

func (c *ChartInspector) Resources(params Parameters) ([]Resource, error) {
	if err := c.Validate(params); err != nil {
		return nil, fmt.Errorf("validating parameters: %w", err)
	}
	u, err := url.JoinPath(c.server, "/resources")
	if err != nil {
		return nil, fmt.Errorf("joining server url: %w", err)
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	query := req.URL.Query()
	setQueryIfNotEmpty(&query, "compositionName", params.CompositionName)
	setQueryIfNotEmpty(&query, "compositionNamespace", params.CompositionNamespace)
	setQueryIfNotEmpty(&query, "compositionDefinitionName", params.CompositionDefinitionName)
	setQueryIfNotEmpty(&query, "compositionDefinitionNamespace", params.CompositionDefinitionNamespace)
	setQueryIfNotEmpty(&query, "compositionDefinitionGroup", params.CompositionDefinitionGroup)
	setQueryIfNotEmpty(&query, "compositionDefinitionVersion", params.CompositionDefinitionVersion)
	setQueryIfNotEmpty(&query, "compositionDefinitionResource", params.CompositionDefinitionResource)
	setQueryIfNotEmpty(&query, "compositionGroup", params.CompositionGroup)
	setQueryIfNotEmpty(&query, "compositionVersion", params.CompositionVersion)
	setQueryIfNotEmpty(&query, "compositionResource", params.CompositionResource)

	req.URL.RawQuery = query.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getting chartinspector: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		bbody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading response body: %w", err)
		}

		return nil, fmt.Errorf("unexpected status code: %d - response body: %s", resp.StatusCode, string(bbody))
	}

	defer resp.Body.Close()

	bbody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var resources []Resource
	if err := json.Unmarshal(bbody, &resources); err != nil {
		return nil, fmt.Errorf("unmarshalling response body: %w", err)
	}

	return resources, nil
}

func setQueryIfNotEmpty(query *url.Values, key, value string) {
	if value != "" {
		query.Set(key, value)
	}
}

package researchgateway

import "encoding/json"

// MarshalCatalog returns deterministic, indented JSON with a trailing newline.
func MarshalCatalog(catalog Catalog) ([]byte, error) { return marshalJSON(catalog) }

func MarshalListResponse(response ListResponse) ([]byte, error) { return marshalJSON(response) }

func MarshalSearchResponse(response SearchResponse) ([]byte, error) { return marshalJSON(response) }

func MarshalReadResponse(response ReadResponse) ([]byte, error) { return marshalJSON(response) }

func MarshalDetail(detail Detail) ([]byte, error) { return marshalJSON(detail) }

func MarshalOverview(overview Overview) ([]byte, error) { return marshalJSON(overview) }

func MarshalUpstreamListResponse(response UpstreamListResponse) ([]byte, error) {
	return marshalJSON(response)
}

func MarshalRecordListResponse(response RecordListResponse) ([]byte, error) {
	return marshalJSON(response)
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

# API versioning policy

Public application endpoints use URL major versions under `/api/v1`. Health, metrics, static assets, and documentation are operational resources and are not versioned. Every successful or error response routed through `/api/v1` includes `API-Version: v1` and a `Link` header that identifies the OpenAPI service description.

Requests under `/api` that do not match a supported version receive a JSON `404` response containing `supported_versions`; they never fall through to the single-page application.

## Compatibility

Changes within a major version must remain backward compatible. Safe changes include adding optional request fields, response fields, endpoints, enum values that clients are documented to tolerate, and new error detail. Removing or renaming fields, changing field types or meaning, making optional fields required, changing authentication semantics, or altering an existing status-code contract requires a new major version.

Clients must ignore unknown response fields, send the documented media type, avoid depending on object key order, and use the versioned URLs returned by the service. The web, admin, and scanner clients are first-party API consumers and must follow the same policy.

## Deprecation

When a replacement major version ships:

1. Publish both versions concurrently and publish a migration guide.
2. Announce deprecation through release notes and customer/operator channels.
3. Add standards-based `Deprecation`, `Sunset`, and successor-version `Link` headers to the old version.
4. Maintain the old version for at least six months unless an actively exploited security issue requires faster action.
5. Measure old-version traffic and contact remaining consumers before sunset.
6. Return `410 Gone` with a migration link only after the announced sunset.

Webhook payloads are provider contracts and are versioned independently. Persisted provider events must remain replayable across application API versions.

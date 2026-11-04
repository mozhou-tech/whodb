# OpenObserve

Select **OpenObserve** in the connection form. Set the host to the server
hostname or HTTP/HTTPS base URL, the username to your OpenObserve email, and the
password to your OpenObserve password. The **Database** field holds the
organization identifier, such as `default`. The default HTTP port is `5080`.
Explicit ports in the host URL take precedence over the Port field. HTTPS URLs
use port 443 unless a non-default port is configured.

The connector supports log stream browsing, declared field metadata, row
pagination, sorting, filtering, SQL search, and CSV export. Streams are read-only
in WhoDB. Creation, ingestion, updates, deletion, graph view, and chat are not
exposed by this connector. TLS uses the system trust store.

All searches and full exports cover the most recent 24 hours by default. Change
**Query lookback (hours)** in the advanced connection settings to adjust this
window. SQL execution returns up to 100 records per request; use the stream
browser to page through records or export the full time window.

The browser reads rows and counts matching records with separate SQL queries
using the same start and end timestamps. OpenObserve's `track_total_hits` flag
rewrites SQL into a count query; it does not add a total to the original rows.
See the [official search request definition](https://github.com/openobserve/openobserve/blob/main/src/config/src/meta/search.rs)
and [count-query rewriter](https://github.com/openobserve/openobserve/blob/main/src/search/src/sql/rewriter/track_total_hits.rs).

## Verification

Unit and HTTP protocol tests:

```sh
cd core
go test -race ./src/plugins/openobserve ./src/sourcecatalog ./src/dbcatalog
```

To run the live integration test against a disposable local instance:

```sh
docker run -d --name whodb-openobserve-test \
  -p 127.0.0.1:15080:5080 \
  -e ZO_ROOT_USER_EMAIL=whodb-test@example.com \
  -e ZO_ROOT_USER_PASSWORD=whodb-test-local \
  -e ZO_TELEMETRY=false \
  public.ecr.aws/zinclabs/openobserve:v0.14.7

# Wait for http://127.0.0.1:15080/healthz to return HTTP 200, then:
cd core
WHODB_TEST_OPENOBSERVE_URL=http://127.0.0.1:15080 \
  go test -v ./src/plugins/openobserve -run TestLiveOpenObserve -count=1

docker rm -f whodb-openobserve-test
```

The live test creates a uniquely named stream, ingests two test records, checks
filtering, pagination, counts, aggregation, and export, and deletes its stream.

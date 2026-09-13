import { tr } from "../lib/i18n";
import { Drawer } from "../overlays";
import { useSearchParams } from "react-router-dom";
import { Button, Select, Table, Timeline } from "tdesign-react";
import { RefreshIcon } from "tdesign-icons-react";
import {
  EmptyState,
  LoadMore,
  PageHeading,
  QueryState,
  StatusBadge,
} from "../components";
import { useAPI, useList } from "../lib/hooks";
import { fmt, label } from "../lib/model";
import type { Incident, Page, Vendor } from "../lib/types";
export default function Incidents() {
  const [params, setParams] = useSearchParams();
  const vendor = params.get("vendor") || "";
  const phase = params.get("phase") || "";
  const id = params.get("detail") || "";
  const listParams = new URLSearchParams();
  if (vendor) listParams.set("vendor", vendor);
  if (phase) listParams.set("phase", phase);
  const query = useList<Incident>(`/incidents?${listParams}`);
  const vendors = useAPI<Page<Vendor>>("/vendors");
  const detail = useAPI<Incident>(`/incidents/${encodeURIComponent(id)}`, !!id);
  const change = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next);
  };
  return (
    <>
      <PageHeading
        title={tr("\u4E8B\u4EF6\u4E2D\u5FC3")}
        description={tr(
          "\u8FFD\u8E2A\u5382\u5546\u4E8B\u4EF6\u7684\u5B8C\u6574\u8FDB\u5C55\uFF0C\u4FDD\u7559\u6BCF\u4E00\u6B21\u5B98\u65B9\u66F4\u65B0\u3002",
        )}
        actions={
          <Button
            variant="outline"
            icon={<RefreshIcon />}
            loading={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {tr("\u5237\u65B0")}
          </Button>
        }
      />
      <section className="panel">
        <div className="filter-row">
          <Select
            aria-label={tr("\u7B5B\u9009\u5382\u5546")}
            filterable
            clearable
            placeholder={tr("\u5168\u90E8\u5382\u5546")}
            value={vendor}
            onChange={(value) => change("vendor", String(value || ""))}
            options={(vendors.data?.data || []).map((v) => ({
              value: v.id,
              label: v.name,
            }))}
          />
          <Select
            aria-label={tr("\u7B5B\u9009\u4E8B\u4EF6\u9636\u6BB5")}
            clearable
            placeholder={tr("\u5168\u90E8\u9636\u6BB5")}
            value={phase}
            onChange={(value) => change("phase", String(value || ""))}
            options={[
              "investigating",
              "identified",
              "monitoring",
              "resolved",
              "unknown",
            ].map((value) => ({ value, label: label(value) }))}
          />
          <Button variant="text" onClick={() => setParams({})}>
            {tr("\u91CD\u7F6E\u7B5B\u9009")}
          </Button>
        </div>
        <QueryState query={query}>
          <Table
            rowKey="id"
            hover
            data={query.rows}
            empty={
              <EmptyState
                title={tr("\u6682\u65E0\u5339\u914D\u4E8B\u4EF6")}
                description={tr(
                  "\u8C03\u6574\u7B5B\u9009\u6761\u4EF6\uFF0C\u6216\u7B49\u5F85\u5382\u5546\u53D1\u5E03\u65B0\u7684\u4E8B\u4EF6\u3002",
                )}
              />
            }
            columns={[
              { colKey: "vendor_name", title: tr("\u5382\u5546"), width: 130 },
              {
                colKey: "name",
                title: tr("\u4E8B\u4EF6"),
                minWidth: 260,
                cell: ({ row }) => (
                  <button
                    className="text-link incident-name"
                    onClick={() => change("detail", row.id)}
                  >
                    {row.name}
                  </button>
                ),
              },
              {
                colKey: "impact",
                title: tr("\u5F71\u54CD\u7EA7\u522B"),
                width: 110,
                cell: ({ row }) => <StatusBadge value={row.impact} />,
              },
              {
                colKey: "phase",
                title: tr("\u5904\u7406\u9636\u6BB5"),
                width: 120,
                cell: ({ row }) => <StatusBadge value={row.phase} />,
              },
              {
                colKey: "updated_at",
                title: tr("\u7CFB\u7EDF\u66F4\u65B0\u65F6\u95F4"),
                minWidth: 180,
                cell: ({ row }) => fmt(row.updated_at),
              },
            ]}
          />
          <LoadMore query={query} />
        </QueryState>
      </section>
      <Drawer
        header={tr("\u4E8B\u4EF6\u8BE6\u60C5")}
        size="640px"
        visible={!!id}
        onClose={() => change("detail", "")}
        footer={null}
      >
        <QueryState query={detail}>
          {detail.data && (
            <div className="drawer-body">
              <div className="detail-eyebrow">{detail.data.vendor_name}</div>
              <h2>{detail.data.name}</h2>
              <div className="badge-row">
                <StatusBadge value={detail.data.impact} />
                <StatusBadge value={detail.data.phase} />
              </div>
              <div className="detail-line">
                <span>{tr("\u5382\u5546\u66F4\u65B0\u65F6\u95F4")}</span>
                {fmt(detail.data.source_updated_at)}
              </div>
              <div className="detail-line">
                <span>{tr("\u7CFB\u7EDF\u53D1\u73B0\u65F6\u95F4")}</span>
                {fmt(detail.data.observed_at)}
              </div>
              <h3>{tr("\u4E8B\u4EF6\u65F6\u95F4\u7EBF")}</h3>
              {detail.data.updates?.length ? (
                <Timeline className="incident-timeline">
                  {[...detail.data.updates]
                    .sort(
                      (a, b) =>
                        Date.parse(b.source_updated_at || b.observed_at) -
                        Date.parse(a.source_updated_at || a.observed_at),
                    )
                    .map((update) => (
                      <Timeline.Item key={update.id}>
                        <time
                          className="incident-timeline-time"
                          dateTime={
                            update.source_updated_at || update.observed_at
                          }
                        >
                          {fmt(update.source_updated_at || update.observed_at)}
                        </time>
                        <strong>{label(update.phase)}</strong>
                        <p className="timeline-body">{update.body}</p>
                        <small className="muted">
                          {tr("\u7CFB\u7EDF\u91C7\u96C6\uFF1A")}
                          {fmt(update.observed_at)}
                        </small>
                      </Timeline.Item>
                    ))}
                </Timeline>
              ) : (
                <EmptyState
                  title={tr("\u6682\u65E0\u66F4\u65B0\u6B63\u6587")}
                  description={tr(
                    "\u5DF2\u663E\u793A\u5F53\u524D\u4E8B\u4EF6\u72B6\u6001\uFF0C\u540E\u7EED\u66F4\u65B0\u5C06\u81EA\u52A8\u540C\u6B65\u3002",
                  )}
                />
              )}
              <details className="advanced-detail">
                <summary>{tr("\u539F\u59CB\u4E8B\u4EF6\u6570\u636E")}</summary>
                <pre>{JSON.stringify(detail.data, null, 2)}</pre>
              </details>
            </div>
          )}
        </QueryState>
      </Drawer>
    </>
  );
}

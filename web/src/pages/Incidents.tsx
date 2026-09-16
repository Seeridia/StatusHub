import { ListToolbar } from "../components";
import { Panel } from "../components";
import { tr } from "../lib/i18n";
import { Drawer } from "../overlays";
import { useSearchParams } from "react-router-dom";
import {
  Button,
  Card,
  Checkbox,
  Collapse,
  Descriptions,
  Select,
  Table,
  Timeline,
  Typography,
} from "tdesign-react";
import { RefreshIcon, LinkIcon } from "tdesign-icons-react";
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
  const includeArchived = params.get("include_archived") === "true";
  const listParams = new URLSearchParams();
  if (vendor) listParams.set("vendor", vendor);
  if (phase) listParams.set("phase", phase);
  if (includeArchived) listParams.set("include_archived", "true");
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
      <Panel className="panel starter-list-panel">
        <ListToolbar
          title={tr("\u4E8B\u4EF6\u4E2D\u5FC3")}
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
        <div className="filter-row">
          <Select
            aria-label={tr("筛选服务")}
            filterable
            clearable
            placeholder={tr("全部服务")}
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
          <Checkbox
            checked={includeArchived}
            onChange={(checked) =>
              change("include_archived", checked ? "true" : "")
            }
          >
            {tr("包含已归档服务")}
          </Checkbox>
        </div>
        <QueryState query={query}>
          <Table
            rowKey="id"
            tableLayout="fixed"
            hover
            data={query.rows}
            empty={
              <EmptyState
                title={tr("\u6682\u65E0\u5339\u914D\u4E8B\u4EF6")}
                description={tr("调整筛选条件，或等待服务发布新的事件。")}
              />
            }
            columns={[
              {
                colKey: "vendor_name",
                title: tr("服务"),
                width: 160,
                ellipsis: true,
              },
              {
                colKey: "name",
                title: tr("\u4E8B\u4EF6"),
                width: 320,
                cell: ({ row }) => (
                  <button
                    className="text-link incident-name"
                    title={row.name}
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
      </Panel>
      <Drawer
        header={tr("\u4E8B\u4EF6\u8BE6\u60C5")}
        size="min(720px, 100vw)"
        visible={!!id}
        onClose={() => change("detail", "")}
        footer={null}
      >
        <QueryState query={detail}>
          {detail.data && (
            <div className="drawer-body incident-detail">
              <Card bordered className="incident-summary">
                <div className="detail-eyebrow">{detail.data.vendor_name}</div>
                <h2>{detail.data.name}</h2>
                <div className="badge-row">
                  <StatusBadge value={detail.data.impact} />
                  <StatusBadge value={detail.data.phase} />
                </div>
                <Descriptions
                  column={1}
                  items={[
                    {
                      label: tr("服务更新时间"),
                      content: fmt(detail.data.source_updated_at),
                    },
                    {
                      label: tr("系统发现时间"),
                      content: fmt(detail.data.observed_at),
                    },
                  ]}
                />
                {detail.data.official_url &&
                  /^https?:\/\//i.test(detail.data.official_url) && (
                    <Button
                      variant="outline"
                      icon={<LinkIcon />}
                      href={detail.data.official_url}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {tr("查看官方状态页")}
                    </Button>
                  )}
              </Card>
              <Card
                bordered
                title={tr("事件时间线")}
                className="incident-history"
              >
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
                          <Typography.Text
                            className="incident-phase-title"
                            strong
                          >
                            {label(update.phase)}
                          </Typography.Text>
                          <time
                            className="incident-timeline-time"
                            dateTime={
                              update.source_updated_at || update.observed_at
                            }
                          >
                            {fmt(
                              update.source_updated_at || update.observed_at,
                            )}
                          </time>
                          <p className="timeline-body">{update.body}</p>
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
              </Card>
              <Collapse className="incident-raw" expandIconPlacement="right">
                <Collapse.Panel value="raw" header={tr("原始事件数据")}>
                  <div className="incident-raw-heading">
                    <Typography.Text
                      copyable={{ text: JSON.stringify(detail.data, null, 2) }}
                    >
                      {tr("复制")}
                    </Typography.Text>
                  </div>
                  <pre tabIndex={0} aria-label={tr("原始事件数据")}>
                    {JSON.stringify(detail.data, null, 2)}
                  </pre>
                </Collapse.Panel>
              </Collapse>
            </div>
          )}
        </QueryState>
      </Drawer>
    </>
  );
}

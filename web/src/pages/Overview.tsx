import { Panel } from "../components";
import { tr } from "../lib/i18n";
import { Drawer } from "../overlays";
import { useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  Button,
  Card,
  Statistic,
  Input,
  Radio,
  Select,
  Table,
  Tag,
} from "tdesign-react";
import {
  ArrowRightIcon,
  CheckCircleIcon,
  CloudIcon,
  ErrorCircleIcon,
  NotificationIcon,
  RefreshIcon,
  SearchIcon,
  TimeIcon,
} from "tdesign-icons-react";
import {
  EmptyState,
  Freshness,
  PageHeading,
  QueryState,
  StatusBadge,
  VendorIdentity,
} from "../components";
import { useAPI, useClock, usePermissions } from "../lib/hooks";
import { fmt, relative, stale } from "../lib/model";
import type { Endpoint, Incident, Page, Vendor } from "../lib/types";
export default function Overview({
  vendorsOnly = false,
}: {
  vendorsOnly?: boolean;
}) {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const now = useClock();
  const permission = usePermissions();
  const [search, setSearch] = useState("");
  const [mode, setMode] = useState("cards");
  const [selected, setSelected] = useState<Vendor | null>(null);
  const query = useAPI<Page<Vendor>>("/vendors");
  const incidents = useAPI<Page<Incident>>("/incidents?limit=8", !vendorsOnly);
  const channels = useAPI<Page<Endpoint>>("/endpoints?limit=200", !vendorsOnly);
  const vendors = query.data?.data || [];
  const filter = params.get("status") || "all";
  const impacted = vendors.filter(
    (v) => !["operational", "unknown"].includes(v.status),
  );
  const expired = vendors.filter((v) => stale(v, now));
  const filtered = useMemo(
    () =>
      vendors
        .filter(
          (v) =>
            `${v.name} ${v.slug}`
              .toLowerCase()
              .includes(search.toLowerCase()) &&
            (filter === "all" ||
              (filter === "stale" && stale(v, now)) ||
              (filter === "impacted" &&
                !["operational", "unknown"].includes(v.status)) ||
              (filter === "normal" &&
                v.status === "operational" &&
                v.collection?.state === "fresh" &&
                !stale(v, now))),
        )
        .sort(
          (a, b) =>
            Number(b.active_incidents > 0) - Number(a.active_incidents > 0) ||
            a.name.localeCompare(b.name),
        ),
    [vendors, search, filter, now],
  );
  const activeCount = vendors.reduce((sum, v) => sum + v.active_incidents, 0);
  const stats = [
    {
      title: tr("\u76D1\u63A7\u5382\u5546"),
      value: vendors.length,
      hint: tr("\u5F53\u524D\u5DE5\u4F5C\u533A\u53EF\u89C1\u7684\u5382\u5546"),
      icon: <CloudIcon />,
      path: "/vendors",
      className: "primary-stat",
    },
    {
      title: tr("\u6D3B\u8DC3\u4E8B\u4EF6"),
      value: activeCount,
      hint: tr("\u5382\u5546\u5C1A\u672A\u7ED3\u675F\u7684\u4E8B\u4EF6"),
      icon: <NotificationIcon />,
      path: "/incidents",
      className: "",
    },
    {
      title: tr("\u53D7\u5F71\u54CD\u5382\u5546"),
      value: impacted.length,
      hint: tr("\u964D\u7EA7\u3001\u4E2D\u65AD\u6216\u7EF4\u62A4\u4E2D"),
      icon: <ErrorCircleIcon />,
      path: "/vendors?status=impacted",
      className: "",
    },
    {
      title: tr("\u6570\u636E\u8FC7\u671F"),
      value: expired.length,
      hint: tr("\u9700\u8981\u68C0\u67E5\u91C7\u96C6\u72B6\u6001"),
      icon: <TimeIcon />,
      path: "/vendors?status=stale",
      className: "",
    },
  ];
  return (
    <>
      {!vendorsOnly && (
        <PageHeading
          title={
            vendorsOnly
              ? tr("\u5382\u5546\u72B6\u6001")
              : tr("\u76D1\u63A7\u603B\u89C8")
          }
          description={
            vendorsOnly
              ? tr(
                  "\u96C6\u4E2D\u67E5\u770B\u5916\u90E8\u4F9D\u8D56\uFF0C\u533A\u5206\u670D\u52A1\u72B6\u6001\u4E0E\u91C7\u96C6\u5065\u5EB7\u3002",
                )
              : tr(
                  "\u638C\u63E1\u5916\u90E8\u4F9D\u8D56\u7684\u6700\u65B0\u72B6\u6001\uFF0C\u8BA9\u91CD\u8981\u53D8\u5316\u4E00\u76EE\u4E86\u7136\u3002",
                )
          }
          actions={
            <>
              <Button
                variant="outline"
                icon={<RefreshIcon />}
                loading={query.isFetching}
                onClick={() => {
                  void query.refetch();
                  void incidents.refetch();
                }}
              >
                {tr("\u5237\u65B0")}
              </Button>
              {permission.write && (
                <Button onClick={() => navigate("/rules/new")}>
                  {tr("\u521B\u5EFA\u901A\u77E5\u89C4\u5219")}
                </Button>
              )}
            </>
          }
        />
      )}
      <QueryState query={query}>
        {!vendorsOnly && (
          <div className="stats-grid">
            {stats.map((stat) => (
              <Card
                key={stat.title}
                className="metric-card"
                bodyClassName="metric-body"
                bordered={false}
              >
                <div className="metric-main">
                  <Statistic
                    title={<span className="metric-title">{stat.title}</span>}
                    value={stat.value}
                    unit={tr("个")}
                    color={
                      stat.className
                        ? "var(--td-brand-color)"
                        : "var(--td-text-color-primary)"
                    }
                  />
                  <span className="metric-icon" aria-hidden="true">
                    {stat.icon}
                  </span>
                </div>
                <a className="metric-link" href={`#${stat.path}`}>
                  <span>{stat.hint}</span>
                  <ArrowRightIcon aria-hidden="true" />
                </a>
              </Card>
            ))}
          </div>
        )}
        {!vendorsOnly && (
          <div
            className={`health-banner ${activeCount || expired.length ? "has-attention" : ""}`}
          >
            <span className="health-icon">
              {activeCount || expired.length ? (
                <ErrorCircleIcon />
              ) : (
                <CheckCircleIcon />
              )}
            </span>
            <div>
              <strong>
                {!vendors.length
                  ? tr(
                      "\u5F00\u59CB\u5EFA\u7ACB\u4F60\u7684\u76D1\u63A7\u89C6\u56FE",
                    )
                  : activeCount
                    ? tr("activeIncidentAttention", { count: activeCount })
                    : expired.length
                      ? tr(
                          "\u5F53\u524D\u6CA1\u6709\u6D3B\u8DC3\u4E8B\u4EF6\uFF0C\u90E8\u5206\u91C7\u96C6\u6570\u636E\u9700\u8981\u68C0\u67E5",
                        )
                      : tr(
                          "\u5F53\u524D\u76D1\u63A7\u8303\u56F4\u5185\u6CA1\u6709\u6D3B\u8DC3\u4E8B\u4EF6",
                        )}
              </strong>
              <p>
                {expired.length
                  ? tr("staleVendorNotice", { count: expired.length })
                  : tr(
                      "\u72B6\u6001\u6765\u81EA\u5382\u5546\u5B98\u65B9\u9875\u9762\uFF0C\u5B9E\u9645\u4E1A\u52A1\u53EF\u7528\u6027\u8BF7\u7ED3\u5408\u81EA\u8EAB\u76D1\u63A7\u5224\u65AD\u3002",
                    )}
              </p>
            </div>
            <Button
              variant="text"
              onClick={() =>
                navigate(expired.length ? "/settings" : "/incidents")
              }
            >
              {expired.length
                ? tr("\u68C0\u67E5\u6570\u636E\u6E90")
                : tr("\u67E5\u770B\u4E8B\u4EF6")}
              <ArrowRightIcon />
            </Button>
          </div>
        )}
        <div className={vendorsOnly ? "" : "overview-grid"}>
          <Panel className="panel vendors-panel">
            <div className="section-head">
              <div>
                <h2>
                  {tr("\u5382\u5546\u72B6\u6001")}
                  <span className="count">{vendors.length}</span>
                </h2>
                <p>
                  {tr(
                    "\u5F02\u5E38\u4F18\u5148\u5C55\u793A \u00B7 \u6700\u8FD1\u6210\u529F\u91C7\u96C6\u65F6\u95F4",
                  )}
                </p>
              </div>
              <div className="view-actions">
                {vendorsOnly && (
                  <div className="heading-actions">
                    <Button
                      variant="outline"
                      icon={<RefreshIcon />}
                      loading={query.isFetching}
                      onClick={() => void query.refetch()}
                    >
                      {tr("刷新")}
                    </Button>
                    {permission.write && (
                      <Button onClick={() => navigate("/rules/new")}>
                        {tr("创建通知规则")}
                      </Button>
                    )}
                  </div>
                )}
                <Radio.Group
                  theme="button"
                  variant="outline"
                  value={mode}
                  onChange={(value) => setMode(String(value))}
                  options={[
                    { value: "cards", label: tr("\u5361\u7247") },
                    { value: "list", label: tr("\u5217\u8868") },
                  ]}
                />
              </div>
            </div>
            <div className="filter-row">
              <Input
                aria-label={tr("\u641C\u7D22\u5382\u5546")}
                prefixIcon={<SearchIcon />}
                placeholder={tr("\u641C\u7D22\u5382\u5546\u540D\u79F0")}
                clearable
                value={search}
                onChange={setSearch}
              />
              <Select
                aria-label={tr("\u5382\u5546\u72B6\u6001\u7B5B\u9009")}
                value={filter}
                onChange={(value) =>
                  setParams(value === "all" ? {} : { status: String(value) })
                }
                options={[
                  { value: "all", label: tr("\u5168\u90E8\u72B6\u6001") },
                  { value: "impacted", label: tr("\u53D7\u5F71\u54CD") },
                  {
                    value: "normal",
                    label: tr("\u6B63\u5E38\u4E14\u6570\u636E\u65B0\u9C9C"),
                  },
                  { value: "stale", label: tr("\u6570\u636E\u8FC7\u671F") },
                ]}
              />
            </div>
            {!filtered.length ? (
              <EmptyState
                title={
                  vendors.length
                    ? tr("\u6CA1\u6709\u5339\u914D\u7684\u5382\u5546")
                    : tr("\u6682\u65E0\u76D1\u63A7\u5382\u5546")
                }
                description={
                  vendors.length
                    ? tr(
                        "\u8BD5\u8BD5\u5176\u4ED6\u540D\u79F0\u6216\u72B6\u6001\u7B5B\u9009\u3002",
                      )
                    : tr(
                        "\u8BF7\u7531\u7BA1\u7406\u5458\u63A5\u5165\u5382\u5546\u6570\u636E\u6E90\u3002",
                      )
                }
              />
            ) : mode === "cards" ? (
              <div className="vendor-grid">
                {filtered.map((v) => (
                  <Card key={v.id} className="vendor-card" bordered>
                    <div className="vendor-card-head">
                      <VendorIdentity vendor={v} />
                      <Button
                        aria-label={tr("\u67E5\u770B {{value0}}", {
                          value0: v.name,
                        })}
                        variant="text"
                        shape="square"
                        icon={<ArrowRightIcon />}
                        onClick={() => setSelected(v)}
                      />
                    </div>
                    <div className="vendor-card-status">
                      <StatusBadge value={v.status} />
                      {v.active_incidents > 0 && (
                        <button
                          className="text-link"
                          onClick={() => navigate(`/incidents?vendor=${v.id}`)}
                        >
                          {tr("activeIncidentCount", {
                            count: v.active_incidents,
                          })}
                        </button>
                      )}
                    </div>
                    <div className="vendor-card-foot">
                      <Freshness vendor={v} />
                      <small>
                        {tr("sourceCount", { count: v.visible_sources })}
                      </small>
                    </div>
                  </Card>
                ))}
              </div>
            ) : (
              <Table
                rowKey="id"
                data={filtered}
                hover
                columns={[
                  {
                    colKey: "name",
                    title: tr("\u5382\u5546"),
                    minWidth: 180,
                    cell: ({ row }) => (
                      <button
                        className="identity-button"
                        onClick={() => setSelected(row)}
                      >
                        <VendorIdentity vendor={row} />
                      </button>
                    ),
                  },
                  {
                    colKey: "status",
                    title: tr("\u670D\u52A1\u72B6\u6001"),
                    minWidth: 125,
                    cell: ({ row }) => <StatusBadge value={row.status} />,
                  },
                  {
                    colKey: "active_incidents",
                    title: tr("\u6D3B\u8DC3\u4E8B\u4EF6"),
                    width: 100,
                  },
                  {
                    colKey: "freshness",
                    title: tr("\u91C7\u96C6\u65B0\u9C9C\u5EA6"),
                    minWidth: 190,
                    cell: ({ row }) => <Freshness vendor={row} />,
                  },
                ]}
              />
            )}
          </Panel>
          {!vendorsOnly && (
            <aside className="overview-aside">
              <Panel className="panel">
                <div className="section-head">
                  <h2>{tr("\u6700\u8FD1\u4E8B\u4EF6")}</h2>
                  <Button variant="text" onClick={() => navigate("/incidents")}>
                    {tr("\u5168\u90E8")}
                    <ArrowRightIcon />
                  </Button>
                </div>
                <QueryState query={incidents}>
                  {incidents.data?.data?.length ? (
                    <div className="activity-list">
                      {incidents.data.data.slice(0, 5).map((i) => (
                        <button
                          className="activity-item"
                          key={i.id}
                          onClick={() => navigate(`/incidents?detail=${i.id}`)}
                        >
                          <div className="activity-meta">
                            <strong>{i.vendor_name}</strong>
                            <StatusBadge value={i.phase} />
                          </div>
                          <p>{i.name}</p>
                          <small>{relative(i.updated_at, now)}</small>
                        </button>
                      ))}
                    </div>
                  ) : (
                    <EmptyState
                      title={tr("\u6682\u65E0\u4E8B\u4EF6")}
                      description={tr(
                        "\u5382\u5546\u4E8B\u4EF6\u66F4\u65B0\u4F1A\u51FA\u73B0\u5728\u8FD9\u91CC\u3002",
                      )}
                    />
                  )}
                </QueryState>
              </Panel>
              <Panel className="panel notification-summary">
                <span className="feature-icon">
                  <NotificationIcon />
                </span>
                <h2>
                  {tr("\u8BA9\u91CD\u8981\u53D8\u5316\u53CA\u65F6\u5230\u8FBE")}
                </h2>
                <p>
                  {channels.isError
                    ? tr(
                        "\u6E20\u9053\u4FE1\u606F\u6682\u65F6\u4E0D\u53EF\u7528\u3002",
                      )
                    : channels.data?.data?.length
                      ? tr("loadedEnabledChannels", {
                          count: channels.data.data.filter((e) => e.enabled)
                            .length,
                          extra: channels.data.next_cursor
                            ? tr("\uFF08\u8FD8\u6709\u66F4\u591A\uFF09")
                            : "",
                        })
                      : tr(
                          "\u8FDE\u63A5 Slack \u6216 Webhook\uFF0C\u4E3A\u5173\u952E\u4F9D\u8D56\u914D\u7F6E\u901A\u77E5\u89C4\u5219\u3002",
                        )}
                </p>
                <Button variant="outline" onClick={() => navigate("/channels")}>
                  {tr("\u7BA1\u7406\u901A\u77E5\u6E20\u9053")}
                  <ArrowRightIcon />
                </Button>
              </Panel>
            </aside>
          )}
        </div>
      </QueryState>
      <Drawer
        header={selected?.name || tr("\u5382\u5546\u8BE6\u60C5")}
        visible={!!selected}
        onClose={() => setSelected(null)}
        footer={null}
        size="560px"
      >
        <div className="drawer-body">
          {selected && (
            <>
              <VendorIdentity vendor={selected} />
              <div className="detail-line">
                <span>{tr("\u670D\u52A1\u72B6\u6001")}</span>
                <StatusBadge value={selected.status} />
              </div>
              <div className="detail-line">
                <span>{tr("\u91C7\u96C6\u5065\u5EB7")}</span>
                <StatusBadge value={selected.source_health_state} />
              </div>
              <div className="detail-line">
                <span>{tr("\u6700\u8FD1\u6210\u529F\u91C7\u96C6")}</span>
                <span>{fmt(selected.last_successful_at)}</span>
              </div>
              <div className="detail-line">
                <span>{tr("\u6D3B\u8DC3\u4E8B\u4EF6")}</span>
                <span>{selected.active_incidents}</span>
              </div>
              <Button
                block
                onClick={() => {
                  navigate(`/incidents?vendor=${selected.id}`);
                  setSelected(null);
                }}
              >
                {tr("\u67E5\u770B\u5173\u8054\u4E8B\u4EF6")}
              </Button>
            </>
          )}
        </div>
      </Drawer>
    </>
  );
}

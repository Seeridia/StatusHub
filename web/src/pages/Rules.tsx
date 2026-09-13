import { Panel } from "../components";
import { DeleteResource } from "../components/DeleteResource";
import { formatList, tr } from "../lib/i18n";
import { FormField } from "../components";
import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import {
  Alert,
  Button,
  Checkbox,
  Form,
  Input,
  Select,
  Switch,
  Table,
  Tag,
} from "tdesign-react";
import {
  AddIcon,
  ArrowLeftIcon,
  ArrowRightIcon,
  CheckCircleIcon,
  NotificationIcon,
  SearchIcon,
  RefreshIcon,
} from "tdesign-icons-react";
import {
  EmptyState,
  LoadMore,
  PageHeading,
  QueryState,
  StatusBadge,
} from "../components";
import { allPages, currentTenant } from "../lib/api";
import { useAPI, useList, usePermissions, useWriter } from "../lib/hooks";
import {
  buildScopes,
  editableScopes,
  eventOptions,
  fmt,
  impactOptions,
  label,
} from "../lib/model";
import type { Endpoint, Page, Subscription, Vendor } from "../lib/types";
import { ChannelEditor } from "./Channels";
function Editor({ id }: { id: string }) {
  const navigate = useNavigate();
  const client = useQueryClient();
  const permission = usePermissions();
  const write = useWriter();
  const detail = useAPI<Subscription>(
    `/subscriptions/${encodeURIComponent(id)}`,
    !!id,
  );
  const vendors = useAPI<Page<Vendor>>("/vendors");
  const endpoints = useQuery({
    queryKey: [currentTenant(), "/endpoints", "all"],
    queryFn: ({ signal }) => allPages<Endpoint>("/endpoints", signal),
  });
  const [name, setName] = useState("");
  const [vendorIDs, setVendorIDs] = useState<string[]>([]);
  const [eventKinds, setEventKinds] = useState<string[]>([
    "incident.created",
    "incident.updated",
    "incident.resolved",
  ]);
  const [impact, setImpact] = useState("unknown");
  const [endpointIDs, setEndpointIDs] = useState<string[]>([]);
  const [enabled, setEnabled] = useState(true);
  const [addingChannel, setAddingChannel] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const original = useRef<Subscription | null>(null);
  const initialized = useRef(false);
  useEffect(() => {
    if (!detail.data || initialized.current) return;
    const value = detail.data;
    original.current = value;
    initialized.current = true;
    setName(value.name);
    setVendorIDs([
      ...new Set(
        (value.scopes || [])
          .map((s) => s.vendor_id)
          .filter((v): v is string => !!v),
      ),
    ]);
    setEventKinds([
      ...new Set(
        (value.scopes || [])
          .map((s) => s.event_kind)
          .filter((v): v is string => !!v),
      ),
    ]);
    setImpact(value.rule.minimum_impact || "unknown");
    setEndpointIDs(value.endpoint_ids || []);
    setEnabled(value.enabled);
  }, [detail.data]);
  const supported =
    !id || !detail.data || editableScopes(detail.data.scopes || []);
  const locked =
    !permission.write ||
    !supported ||
    (!!id && !original.current) ||
    vendors.isPending ||
    endpoints.isPending ||
    vendors.isError ||
    endpoints.isError;
  const vendorNames = vendorIDs.map(
    (key) => vendors.data?.data?.find((v) => v.id === key)?.name || key,
  );
  const channelNames = endpointIDs.map(
    (key) => endpoints.data?.find((e) => e.id === key)?.name || key,
  );
  async function save() {
    if (locked || busy) return;
    if (!name.trim()) {
      setError(tr("\u8BF7\u8F93\u5165\u89C4\u5219\u540D\u79F0\u3002"));
      return;
    }
    if (!endpointIDs.length) {
      setError(
        tr(
          "\u8BF7\u81F3\u5C11\u9009\u62E9\u4E00\u4E2A\u901A\u77E5\u6E20\u9053\u3002",
        ),
      );
      return;
    }
    setBusy(true);
    setError("");
    try {
      await write(
        `/subscriptions${id ? `/${id}` : ""}`,
        {
          name: name.trim(),
          enabled,
          ...(id
            ? { expected_rule_version: original.current!.rule_version }
            : {}),
          rule: { ...(original.current?.rule || {}), minimum_impact: impact },
          scopes: buildScopes(vendorIDs, eventKinds),
          endpoint_ids: endpointIDs,
        },
        id ? "PUT" : "POST",
      );
      await client.invalidateQueries({
        predicate: (q) =>
          q.queryKey[0] === currentTenant() &&
          String(q.queryKey[1]).startsWith("/subscriptions"),
      });
      navigate("/rules?saved=1");
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <PageHeading
        title={
          id
            ? tr("\u7F16\u8F91\u901A\u77E5\u89C4\u5219")
            : tr("\u521B\u5EFA\u901A\u77E5\u89C4\u5219")
        }
        description={tr(
          "\u9009\u62E9\u8981\u5173\u6CE8\u7684\u53D8\u5316\uFF0C\u4EE5\u53CA\u5B83\u4EEC\u5E94\u8BE5\u5230\u8FBE\u7684\u5730\u65B9\u3002",
        )}
        actions={
          <Button
            variant="text"
            icon={<ArrowLeftIcon />}
            onClick={() => navigate("/rules")}
          >
            {tr("\u8FD4\u56DE\u89C4\u5219\u5217\u8868")}
          </Button>
        }
      />
      {!supported && (
        <Alert
          className="query-error"
          theme="warning"
          message={tr(
            "\u6B64\u89C4\u5219\u5305\u542B\u7CBE\u7EC6\u670D\u52A1\u8303\u56F4\u6216\u975E\u7EC4\u5408\u6761\u4EF6\uFF0C\u672C\u7F16\u8F91\u5668\u6682\u4E0D\u652F\u6301\u4FEE\u6539\uFF0C\u4EE5\u514D\u6269\u5927\u901A\u77E5\u8303\u56F4\u3002\u8BF7\u901A\u8FC7 API \u7BA1\u7406\u3002",
          )}
        />
      )}
      {!!id && detail.isError && (
        <Alert theme="error" message={detail.error.message} />
      )}
      {(vendors.isError || endpoints.isError) && (
        <Alert
          className="query-error"
          theme="error"
          message={tr(
            "\u65E0\u6CD5\u52A0\u8F7D\u5382\u5546\u6216\u6E20\u9053\u9009\u9879\uFF0C\u8BF7\u91CD\u8BD5\u540E\u4FDD\u5B58\u3002",
          )}
          operation={
            <Button
              variant="text"
              onClick={() => {
                void vendors.refetch();
                void endpoints.refetch();
              }}
            >
              {tr("\u91CD\u8BD5")}
            </Button>
          }
        />
      )}
      <div className="editor-grid">
        <Form
          labelAlign="top"
          layout="vertical"
          className="rule-form"
          onSubmit={() => void save()}
        >
          <Panel className="panel">
            <div className="form-section-title">
              <Tag theme="primary" variant="light">
                01
              </Tag>
              <div>
                <h2>{tr("\u57FA\u672C\u4FE1\u606F")}</h2>
                <p>
                  {tr(
                    "\u7ED9\u89C4\u5219\u4E00\u4E2A\u5BB9\u6613\u8FA8\u8BA4\u7684\u540D\u79F0",
                  )}
                </p>
              </div>
            </div>
            <FormField label={tr("\u89C4\u5219\u540D\u79F0")} name="name">
              <Input
                aria-label={tr("\u89C4\u5219\u540D\u79F0")}
                value={name}
                onChange={setName}
                placeholder={tr(
                  "\u4F8B\u5982 \u751F\u4EA7\u73AF\u5883 \u00B7 AI \u670D\u52A1\u544A\u8B66",
                )}
                maxlength={200}
                disabled={locked}
              />
            </FormField>
            <div className="setting-row">
              <div>
                <strong>{tr("\u542F\u7528\u89C4\u5219")}</strong>
                <p>
                  {tr(
                    "\u5173\u95ED\u540E\u4FDD\u7559\u914D\u7F6E\uFF0C\u6682\u505C\u5339\u914D\u65B0\u7684\u4E8B\u4EF6\u3002",
                  )}
                </p>
              </div>
              <Switch
                aria-label={tr("\u542F\u7528\u89C4\u5219")}
                aria-checked={enabled}
                value={enabled}
                onChange={(v) => setEnabled(Boolean(v))}
                disabled={locked}
              />
            </div>
          </Panel>
          <Panel className="panel">
            <div className="form-section-title">
              <Tag theme="primary" variant="light">
                02
              </Tag>
              <div>
                <h2>{tr("\u76D1\u63A7\u8303\u56F4\u4E0E\u6761\u4EF6")}</h2>
                <p>
                  {tr(
                    "\u53EA\u63A5\u6536\u4E0E\u4F60\u7684\u4F9D\u8D56\u6709\u5173\u7684\u53D8\u5316",
                  )}
                </p>
              </div>
            </div>
            <FormField
              label={tr("\u5173\u6CE8\u5382\u5546")}
              name="vendors"
              help={tr(
                "\u7559\u7A7A\u8868\u793A\u5F53\u524D\u5DE5\u4F5C\u533A\u53EF\u89C1\u7684\u5168\u90E8\u5382\u5546\uFF0C\u4E5F\u5305\u542B\u540E\u7EED\u65B0\u589E\u5382\u5546\u3002",
              )}
            >
              <Select
                aria-label={tr("\u5173\u6CE8\u5382\u5546")}
                multiple
                filterable
                clearable
                value={vendorIDs}
                onChange={(v) => setVendorIDs(v as string[])}
                placeholder={tr(
                  "\u9009\u62E9\u5382\u5546\uFF0C\u7559\u7A7A\u8868\u793A\u5168\u90E8",
                )}
                options={(vendors.data?.data || []).map((v) => ({
                  value: v.id,
                  label: v.name,
                }))}
                disabled={locked}
              />
            </FormField>
            <FormField
              label={tr("\u6700\u4F4E\u5F71\u54CD\u7EA7\u522B")}
              name="impact"
            >
              <Select
                aria-label={tr("\u6700\u4F4E\u5F71\u54CD\u7EA7\u522B")}
                value={impact}
                onChange={(v) => setImpact(String(v))}
                options={impactOptions}
                disabled={locked}
              />
            </FormField>
            <FormField
              label={tr("\u4E8B\u4EF6\u7C7B\u578B")}
              name="events"
              help={tr(
                "\u5168\u90E8\u53D6\u6D88\u8868\u793A\u4E0D\u9650\u4E8B\u4EF6\u7C7B\u578B\u3002",
              )}
            >
              <Checkbox.Group
                aria-label={tr("\u4E8B\u4EF6\u7C7B\u578B")}
                className="event-checkboxes"
                value={eventKinds}
                onChange={(v) => setEventKinds(v as string[])}
                options={eventOptions}
                disabled={locked}
              />
            </FormField>
            {impact !== "unknown" &&
              eventKinds.includes("incident.resolved") && (
                <Alert
                  theme="warning"
                  message={tr(
                    "\u6700\u4F4E\u5F71\u54CD\u6761\u4EF6\u540C\u6837\u7528\u4E8E\u6062\u590D\u4E8B\u4EF6\u3002\u6062\u590D\u66F4\u65B0\u82E5\u88AB\u6807\u8BB0\u4E3A\u65E0\u5F71\u54CD\uFF0C\u53EF\u80FD\u4E0D\u4F1A\u53D1\u9001\uFF1B\u9700\u8981\u5B8C\u6574\u6062\u590D\u901A\u77E5\u65F6\u8BF7\u9009\u62E9\u6240\u6709\u5F71\u54CD\u7EA7\u522B\u3002",
                  )}
                />
              )}
            {original.current &&
              (original.current.rule.quiet_hours ||
                original.current.rule.include_keywords?.length ||
                original.current.rule.exclude_keywords?.length) && (
                <Alert
                  theme="info"
                  message={tr(
                    "\u6B64\u89C4\u5219\u5DF2\u6709\u7684\u5173\u952E\u8BCD\u548C\u9759\u9ED8\u65F6\u6BB5\u914D\u7F6E\u4F1A\u4FDD\u7559\u3002",
                  )}
                />
              )}
          </Panel>
          <Panel className="panel">
            <div className="form-section-title">
              <Tag theme="primary" variant="light">
                03
              </Tag>
              <div>
                <h2>{tr("\u63A5\u6536\u6E20\u9053")}</h2>
                <p>
                  {tr(
                    "\u540C\u4E00\u89C4\u5219\u53EF\u4EE5\u53D1\u9001\u5230\u591A\u4E2A\u63A5\u6536\u6E20\u9053",
                  )}
                </p>
              </div>
            </div>
            <FormField
              label={tr("\u901A\u77E5\u53D1\u9001\u81F3")}
              name="endpoints"
            >
              <Select
                aria-label={tr("\u901A\u77E5\u53D1\u9001\u81F3")}
                multiple
                filterable
                value={endpointIDs}
                onChange={(v) => setEndpointIDs(v as string[])}
                placeholder={tr("\u8BF7\u9009\u62E9\u901A\u77E5\u6E20\u9053")}
                options={(endpoints.data || []).map((e) => ({
                  value: e.id,
                  label: tr("{{value0}} \u00B7 {{value1}}{{value2}}", {
                    value0: e.name,
                    value1: label(e.channel),
                    value2: e.enabled
                      ? ""
                      : tr("\uFF08\u5DF2\u505C\u7528\uFF09"),
                  }),
                  disabled: !e.enabled,
                }))}
                disabled={locked}
              />
            </FormField>
            {permission.write && (
              <Button
                variant="text"
                icon={<AddIcon />}
                onClick={() => setAddingChannel(true)}
              >
                {tr("\u6DFB\u52A0\u65B0\u6E20\u9053")}
              </Button>
            )}
            {!endpoints.isPending && !endpoints.data?.length && (
              <p className="field-hint">
                {tr(
                  "\u8FD8\u6CA1\u6709\u6E20\u9053\uFF1F\u53EF\u76F4\u63A5\u5728\u8FD9\u91CC\u6DFB\u52A0\uFF0C\u5DF2\u586B\u5199\u7684\u89C4\u5219\u4F1A\u4FDD\u7559\u3002",
                )}
              </p>
            )}
          </Panel>
          {error && <Alert theme="error" message={error} />}
          <div className="editor-actions">
            <Button
              variant="outline"
              onClick={() => navigate("/rules")}
              disabled={busy}
            >
              {tr("\u53D6\u6D88")}
            </Button>
            <Button type="submit" loading={busy} disabled={locked}>
              {id
                ? tr("\u4FDD\u5B58\u4FEE\u6539")
                : tr("\u521B\u5EFA\u89C4\u5219")}
            </Button>
          </div>
        </Form>
        <aside className="rule-summary panel">
          <span className="feature-icon">
            <NotificationIcon />
          </span>
          <h2>{tr("\u89C4\u5219\u9884\u89C8")}</h2>
          <p className="muted">
            {tr("\u4F60\u7684\u914D\u7F6E\u5C06\u5982\u4F55\u5DE5\u4F5C")}
          </p>
          <div className="summary-step">
            <small>{tr("\u5173\u6CE8\u8FD9\u4E9B\u5382\u5546")}</small>
            <strong>
              {vendorNames.length
                ? formatList(vendorNames)
                : tr("\u5168\u90E8\u53EF\u89C1\u5382\u5546")}
            </strong>
          </div>
          <ArrowRightIcon className="summary-arrow" />
          <div className="summary-step">
            <small>{tr("\u53D1\u751F\u8FD9\u4E9B\u53D8\u5316")}</small>
            <strong>
              {eventKinds.length
                ? formatList(eventKinds.map(label))
                : tr("\u6240\u6709\u4E8B\u4EF6\u7C7B\u578B")}
            </strong>
            <Tag variant="light">
              {impactOptions.find((o) => o.value === impact)?.label}
            </Tag>
          </div>
          <ArrowRightIcon className="summary-arrow" />
          <div className="summary-step">
            <small>{tr("\u901A\u77E5\u53D1\u9001\u81F3")}</small>
            <strong>
              {channelNames.length
                ? formatList(channelNames)
                : tr("\u5C1A\u672A\u9009\u62E9\u6E20\u9053")}
            </strong>
          </div>
          <div className="summary-status">
            <CheckCircleIcon />
            {enabled
              ? tr(
                  "\u4FDD\u5B58\u540E\u5F00\u59CB\u5339\u914D\u65B0\u4E8B\u4EF6",
                )
              : tr("\u4FDD\u5B58\u4E3A\u505C\u7528\u72B6\u6001")}
          </div>
        </aside>
      </div>
      <ChannelEditor
        visible={addingChannel}
        onClose={() => setAddingChannel(false)}
        onSaved={(endpoint) =>
          setEndpointIDs((previous) => [...previous, endpoint.id])
        }
      />
    </>
  );
}
export default function Rules() {
  const location = useLocation();
  const navigate = useNavigate();
  const permission = usePermissions();
  const [params] = useSearchParams();
  const query = useList<Subscription>("/subscriptions");
  const [search, setSearch] = useState("");
  const [current, setCurrent] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const rows = query.rows.filter((row) =>
    row.name.toLowerCase().includes(search.trim().toLowerCase()),
  );
  const editing =
    location.pathname !== "/rules" && location.pathname !== "/rules/";
  if (editing)
    return (
      <Editor
        key={location.pathname}
        id={
          location.pathname.endsWith("/new")
            ? ""
            : location.pathname.split("/").at(-1)!
        }
      />
    );
  return (
    <>
      {params.has("saved") && (
        <Alert
          className="query-error"
          theme="success"
          message={tr("\u901A\u77E5\u89C4\u5219\u5DF2\u4FDD\u5B58\u3002")}
        />
      )}
      <Panel className="panel starter-list-panel">
        <div className="starter-list-toolbar">
          <div className="heading-actions">
            {permission.write && (
              <Button icon={<AddIcon />} onClick={() => navigate("/rules/new")}>
                {tr("创建规则")}
              </Button>
            )}
            <Button
              theme="default"
              variant="outline"
              icon={<RefreshIcon />}
              loading={query.isFetching}
              onClick={() => void query.refetch()}
            >
              {tr("刷新")}
            </Button>
          </div>
          <Input
            className="starter-list-search"
            aria-label={tr("搜索已加载的规则")}
            placeholder={tr("搜索已加载的规则")}
            suffixIcon={<SearchIcon />}
            clearable
            value={search}
            onChange={(value) => {
              setSearch(value);
              setCurrent(1);
            }}
          />
        </div>
        {query.hasNextPage && (
          <p className="field-hint">
            {tr("当前仅显示已加载的规则，可在下方继续加载。")}
          </p>
        )}

        <QueryState query={query}>
          <Table
            tableLayout="fixed"
            rowKey="id"
            data={rows}
            hover
            pagination={{
              current: Math.min(
                current,
                Math.max(1, Math.ceil(rows.length / pageSize)),
              ),
              pageSize,
              total: rows.length,
              showJumper: true,
              onCurrentChange: setCurrent,
              onPageSizeChange: (size) => {
                setPageSize(size);
                setCurrent(1);
              },
            }}
            empty={
              search ? (
                <EmptyState
                  title={tr("没有匹配的规则")}
                  description={tr("尝试其他名称，或清空搜索条件。")}
                />
              ) : (
                <EmptyState
                  title={tr(
                    "\u521B\u5EFA\u4F60\u7684\u7B2C\u4E00\u6761\u901A\u77E5\u89C4\u5219",
                  )}
                  description={tr(
                    "\u9009\u62E9\u5382\u5546\u4E0E\u6E20\u9053\uFF0C\u8BA9\u91CD\u8981\u7684\u72B6\u6001\u53D8\u5316\u53CA\u65F6\u5230\u8FBE\u3002",
                  )}
                  action={
                    permission.write && (
                      <Button onClick={() => navigate("/rules/new")}>
                        {tr("\u521B\u5EFA\u901A\u77E5\u89C4\u5219")}
                      </Button>
                    )
                  }
                />
              )
            }
            columns={[
              {
                colKey: "name",
                title: tr("\u89C4\u5219\u540D\u79F0"),
                width: 180,
                cell: ({ row }) => (
                  <button
                    className="text-link"
                    onClick={() => navigate(`/rules/${row.id}`)}
                  >
                    {row.name}
                  </button>
                ),
              },
              {
                colKey: "enabled",
                title: tr("\u72B6\u6001"),
                width: 90,
                cell: ({ row }) => (
                  <StatusBadge value={row.enabled ? "enabled" : "disabled"} />
                ),
              },
              {
                colKey: "rule",
                title: tr("\u6700\u4F4E\u5F71\u54CD"),
                width: 120,
                cell: ({ row }) =>
                  row.rule.minimum_impact === "unknown" ||
                  !row.rule.minimum_impact
                    ? tr("\u6240\u6709\u5F71\u54CD\u7EA7\u522B")
                    : tr("{{value0}}\u53CA\u4EE5\u4E0A", {
                        value0: label(row.rule.minimum_impact),
                      }),
              },
              {
                colKey: "endpoint_ids",
                title: tr("\u63A5\u6536\u6E20\u9053"),
                width: 90,
                cell: ({ row }) =>
                  tr("channelCount", {
                    count: row.endpoint_ids?.length || 0,
                  }),
              },
              {
                colKey: "updated_at",
                title: tr("\u66F4\u65B0\u65F6\u95F4"),
                width: 200,
                cell: ({ row }) => fmt(row.updated_at),
              },
              {
                colKey: "actions",
                title: tr("操作"),
                width: 180,
                fixed: "right",
                cell: ({ row }) => (
                  <div className="table-actions">
                    <Button variant="text" href={`#/rules/${row.id}`}>
                      {permission.write ? tr("编辑") : tr("查看")}
                    </Button>
                    <DeleteResource
                      path={`/subscriptions/${row.id}`}
                      name={row.name}
                      disabled={!permission.write}
                    />
                  </div>
                ),
              },
            ]}
          />
          <LoadMore query={query} />
        </QueryState>
      </Panel>
    </>
  );
}

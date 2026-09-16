import { ListToolbar } from "../components";
import { Panel } from "../components";
import { tr } from "../lib/i18n";
import { FormField, ValidatedForm } from "../components";
import { Drawer } from "../overlays";
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Dropdown,
  Input,
  Select,
  Switch,
  Table,
  Tabs,
} from "tdesign-react";
import { AddIcon, RefreshIcon } from "tdesign-icons-react";
import {
  EmptyState,
  LoadMore,
  PageHeading,
  QueryState,
  StatusBadge,
} from "../components";
import { api, currentTenant } from "../lib/api";
import { useList, usePermissions, useWriter } from "../lib/hooks";
import { fmt, label } from "../lib/model";
import type { Endpoint, TestJob } from "../lib/types";
export function ChannelEditor({
  visible,
  endpoint,
  onClose,
  onSaved,
}: {
  visible: boolean;
  endpoint?: Endpoint | null;
  onClose: () => void;
  onSaved?: (endpoint: Endpoint) => void;
}) {
  const client = useQueryClient();
  const write = useWriter();
  const [name, setName] = useState("");
  const [channel, setChannel] = useState("slack");
  const [url, setURL] = useState("");
  const [key, setKey] = useState("primary");
  const [secret, setSecret] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [error, setError] = useState("");
  const [smtpAddress, setSMTPAddress] = useState("");
  const [smtpUsername, setSMTPUsername] = useState("");
  const [smtpSecurity, setSMTPSecurity] = useState("starttls");
  const [mailFrom, setMailFrom] = useState("");
  const [mailTo, setMailTo] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (visible) {
      setSMTPAddress("");
      setSMTPUsername("");
      setSMTPSecurity("starttls");
      setMailFrom("");
      setMailTo("");
      setName(endpoint?.name || "");
      setURL("");
      setSecret("");
      setError("");
      setChannel(endpoint?.channel || "slack");
      setEnabled(endpoint?.enabled ?? true);
      setKey("primary");
      if (endpoint?.id) {
        void api<Endpoint>("/endpoints/" + endpoint.id)
          .then((detail) => {
            setName(detail.name);
            setChannel(detail.channel);
            setEnabled(detail.enabled);
            setURL(detail.config?.url || "");
            setKey(detail.config?.signing_key_id || "primary");
            setSMTPAddress(detail.config?.smtp_address || "");
            setSMTPUsername(detail.config?.smtp_username || "");
            setSMTPSecurity(detail.config?.smtp_security || "starttls");
            setMailFrom(detail.config?.from || "");
            setMailTo(detail.config?.to || "");
          })
          .catch((err) => setError((err as Error).message));
      }
    }
  }, [visible, endpoint]);
  function validate() {
    const errors: Record<string, string> = {};
    if (!name.trim())
      errors.name = tr(
        "\u8BF7\u4E3A\u901A\u77E5\u6E20\u9053\u547D\u540D\u3002",
      );
    let valid = false;
    try {
      const parsed = new URL(url);
      valid =
        parsed.protocol === "https:" && !parsed.username && !parsed.password;
    } catch {
      /* invalid URL */
    }
    if (channel !== "smtp" && !valid && !(endpoint && !url.trim()))
      errors.url = tr(
        "\u8BF7\u8F93\u5165\u6709\u6548\u7684 HTTPS \u5730\u5740\uFF0C\u4E14\u4E0D\u8981\u5728\u5730\u5740\u4E2D\u5305\u542B\u7528\u6237\u540D\u548C\u5BC6\u7801\u3002",
      );
    if (channel === "generic_webhook") {
      if (!key.trim())
        errors.key = tr(
          "Webhook \u9700\u8981\u7B7E\u540D\u6807\u8BC6\u4E0E\u7B7E\u540D\u5BC6\u94A5\u3002",
        );
      if (!endpoint && !secret.trim())
        errors.secret = tr(
          "Webhook \u9700\u8981\u7B7E\u540D\u6807\u8BC6\u4E0E\u7B7E\u540D\u5BC6\u94A5\u3002",
        );
    }
    if (channel === "lark" && !endpoint && !secret.trim())
      errors.secret = tr("请填写飞书机器人的签名密钥。");
    if (channel === "smtp") {
      if (!/^[^\s/:]+:(465|587|2525)$/.test(smtpAddress.trim()))
        errors.smtp_address = tr(
          "请填写 SMTP 服务器及端口，例如 smtp.example.com:587。",
        );
      if (!/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(mailFrom.trim()))
        errors.from = tr("请输入有效的邮箱地址。");
      if (!/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(mailTo.trim()))
        errors.to = tr("请输入有效的邮箱地址。");
      if (smtpUsername.trim() && !endpoint && !secret)
        errors.secret = tr("请填写 SMTP 密码或授权码。");
    }
    return errors;
  }
  async function save() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const saved = await write<Endpoint>(
        endpoint ? "/endpoints/" + endpoint.id : "/endpoints",
        {
          name: name.trim(),
          channel,
          enabled,
          ...(endpoint
            ? { expected_secret_version: endpoint.secret_version }
            : {}),
          config: {
            ...(channel === "smtp"
              ? {
                  smtp_address: smtpAddress.trim(),
                  smtp_username: smtpUsername.trim(),
                  smtp_security: smtpSecurity,
                  from: mailFrom.trim(),
                  to: mailTo.trim(),
                  secret,
                }
              : { url: url.trim() }),
            ...(channel === "generic_webhook"
              ? { signing_key_id: key.trim(), secret }
              : channel === "lark"
                ? { secret: secret.trim() }
                : {}),
          },
        },
        endpoint ? "PUT" : "POST",
      );
      await client.invalidateQueries({
        predicate: (q) =>
          q.queryKey[0] === currentTenant() &&
          String(q.queryKey[1]).startsWith("/endpoints"),
      });
      setSecret("");
      onSaved?.(saved);
      onClose();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Drawer
      header={
        endpoint
          ? tr("编辑通知渠道")
          : tr("\u6DFB\u52A0\u901A\u77E5\u6E20\u9053")
      }
      size="520px"
      visible={visible}
      onClose={() => !busy && onClose()}
      closeOnOverlayClick={!busy}
      footer={null}
    >
      <div className="drawer-body">
        <p className="muted">
          {tr("连接团队的接收渠道，再通过通知规则关联服务。")}
        </p>
        <ValidatedForm
          key={String(visible)}
          validate={validate}
          labelAlign="top"
          layout="vertical"
          onSubmit={() => void save()}
        >
          <FormField
            label={tr("\u6E20\u9053\u540D\u79F0")}
            name="name"
            required
          >
            <Input
              aria-label={tr("\u6E20\u9053\u540D\u79F0")}
              placeholder={tr("\u4F8B\u5982 \u751F\u4EA7\u544A\u8B66")}
              value={name}
              onChange={setName}
              maxlength={200}
            />
          </FormField>
          <FormField label={tr("\u6E20\u9053\u7C7B\u578B")} name="channel">
            <Select
              aria-label={tr("\u6E20\u9053\u7C7B\u578B")}
              value={channel}
              disabled={!!endpoint}
              onChange={(v) => setChannel(String(v))}
              options={[
                { value: "slack", label: "Slack Incoming Webhook" },
                { value: "lark", label: tr("飞书") },
                { value: "smtp", label: tr("SMTP 邮件") },
                { value: "generic_webhook", label: tr("\u901A\u7528 Webhook") },
              ]}
            />
          </FormField>
          <FormField label={tr("启用状态")} name="enabled">
            <Switch value={enabled} onChange={setEnabled} />
          </FormField>
          {channel !== "smtp" && (
            <FormField
              label={tr("HTTPS \u5730\u5740")}
              name="url"
              required={!endpoint}
            >
              <Input
                aria-label={tr("HTTPS \u5730\u5740")}
                placeholder={
                  endpoint
                    ? tr("留空以保留现有凭据")
                    : channel === "slack"
                      ? "https://hooks.slack.com/services/…"
                      : "https://example.com/webhook"
                }
                value={url}
                onChange={setURL}
              />
            </FormField>
          )}
          {(channel === "generic_webhook" || channel === "lark") && (
            <>
              {channel === "generic_webhook" && (
                <FormField
                  label={tr("\u7B7E\u540D\u5BC6\u94A5\u6807\u8BC6")}
                  name="key"
                  required
                >
                  <Input
                    aria-label={tr("\u7B7E\u540D\u5BC6\u94A5\u6807\u8BC6")}
                    value={key}
                    onChange={setKey}
                  />
                </FormField>
              )}
              <FormField
                label={tr("\u7B7E\u540D\u5BC6\u94A5")}
                name="secret"
                required={!endpoint}
              >
                <Input
                  aria-label={tr("\u7B7E\u540D\u5BC6\u94A5")}
                  type="password"
                  autocomplete="off"
                  value={secret}
                  onChange={setSecret}
                />
              </FormField>
            </>
          )}
          {channel === "smtp" && (
            <>
              <FormField
                label={tr("SMTP 服务器及端口")}
                name="smtp_address"
                required
              >
                <Input
                  value={smtpAddress}
                  onChange={setSMTPAddress}
                  placeholder="smtp.example.com:587"
                />
              </FormField>
              <FormField label={tr("连接加密")} name="smtp_security">
                <Select
                  value={smtpSecurity}
                  onChange={(v) => setSMTPSecurity(String(v))}
                  options={[
                    { value: "starttls", label: "STARTTLS (587 / 2525)" },
                    { value: "tls", label: "TLS (465)" },
                  ]}
                />
              </FormField>
              <FormField label={tr("SMTP 用户名")} name="smtp_username">
                <Input
                  value={smtpUsername}
                  onChange={setSMTPUsername}
                  autocomplete="off"
                />
              </FormField>
              <FormField
                label={tr("SMTP 密码或授权码")}
                name="secret"
                required={!endpoint && !!smtpUsername.trim()}
              >
                <Input
                  type="password"
                  value={secret}
                  onChange={setSecret}
                  autocomplete="new-password"
                  placeholder={endpoint ? tr("留空以保留现有凭据") : undefined}
                />
              </FormField>
              <FormField label={tr("发件邮箱")} name="from" required>
                <Input
                  value={mailFrom}
                  onChange={setMailFrom}
                  autocomplete="email"
                />
              </FormField>
              <FormField label={tr("收件邮箱")} name="to" required>
                <Input
                  value={mailTo}
                  onChange={setMailTo}
                  autocomplete="email"
                />
              </FormField>
              <Alert
                theme="info"
                message={tr(
                  "使用加密 SMTP 连接发送 HTML 邮件及纯文本副本。每个渠道配置一个收件邮箱，可使用团队邮件组。与账号邀请邮件配置独立。",
                )}
              />
            </>
          )}
          {channel === "lark" && (
            <Alert
              theme="info"
              message={tr(
                "使用飞书群自定义机器人的 Webhook 地址，并开启签名校验。签名密钥填写机器人安全设置中的密钥。保存后可发送测试通知。",
              )}
            />
          )}
          {error && <Alert theme="error" message={error} />}
          <Alert
            theme="info"
            message={tr(
              "\u51ED\u636E\u52A0\u5BC6\u4FDD\u5B58\uFF0C\u4FDD\u5B58\u540E\u4E0D\u4F1A\u518D\u6B21\u663E\u793A\u3002\u4FDD\u5B58\u4E0D\u4F1A\u53D1\u9001\u901A\u77E5\uFF0C\u53EF\u5728\u6E20\u9053\u5217\u8868\u53D1\u8D77\u6D4B\u8BD5\u3002",
            )}
          />
          <div className="form-actions">
            <Button variant="outline" onClick={onClose} disabled={busy}>
              {tr("\u53D6\u6D88")}
            </Button>
            <Button type="submit" loading={busy}>
              {tr("\u4FDD\u5B58\u6E20\u9053")}
            </Button>
          </div>
        </ValidatedForm>
      </div>
    </Drawer>
  );
}
export default function Channels() {
  const [tab, setTab] = useState("active");
  const query = useList<Endpoint>(
    "/endpoints?state=" + (tab === "archived" ? "archived" : "active"),
  );
  const permission = usePermissions();
  const write = useWriter();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Endpoint | null>(null);
  const [testing, setTesting] = useState(false);
  const [job, setJob] = useState<TestJob | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const result = useQuery({
    queryKey: [currentTenant(), "endpoint-test", job?.id],
    enabled: !!job,
    queryFn: () => api<TestJob>(`/endpoint-tests/${job!.id}`),
    refetchInterval: (q) =>
      q.state.data && ["succeeded", "failed"].includes(q.state.data.status)
        ? false
        : 1500,
  });
  async function test(endpoint: Endpoint) {
    setTesting(true);
    setError("");
    setNotice("");
    setJob(null);
    try {
      setJob(await write<TestJob>(`/endpoints/${endpoint.id}/test`, {}));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setTesting(false);
    }
  }
  const pending =
    !!job &&
    !["succeeded", "failed"].includes(result.data?.status || job.status);
  const outcome = result.data || job;
  async function archive(endpoint: Endpoint) {
    setError("");
    try {
      await write("/endpoints/" + endpoint.id, {}, "DELETE");
      setNotice(tr("通知渠道已归档，历史投递记录仍然保留。"));
      await query.refetch();
    } catch (err) {
      setError((err as Error).message);
    }
  }
  async function restore(endpoint: Endpoint) {
    setError("");
    try {
      await write("/endpoints/" + endpoint.id + "/restore", {});
      setNotice(tr("通知渠道已恢复为停用状态，请检查凭据后再启用。"));
      await query.refetch();
    } catch (err) {
      setError((err as Error).message);
    }
  }
  return (
    <>
      {error && <Alert className="query-error" theme="error" message={error} />}
      {notice && (
        <Alert className="query-error" theme="success" message={notice} />
      )}
      {outcome && (
        <Alert
          className="query-error"
          theme={
            outcome.status === "failed"
              ? "error"
              : outcome.status === "succeeded"
                ? "success"
                : "info"
          }
          title={tr("\u6E20\u9053\u6D4B\u8BD5\uFF1A{{value0}}", {
            value0: label(outcome.status),
          })}
          message={
            outcome.error_summary ||
            (outcome.http_status
              ? tr("\u63A5\u6536\u7AEF\u8FD4\u56DE HTTP {{value0}}", {
                  value0: outcome.http_status,
                })
              : tr(
                  "\u6D4B\u8BD5\u4EFB\u52A1\u5DF2\u5165\u961F\uFF0C\u6B63\u5728\u7B49\u5F85\u901A\u77E5\u5DE5\u4F5C\u8FDB\u7A0B\u5904\u7406\u3002",
                ))
          }
        />
      )}
      {result.isError && (
        <Alert
          className="query-error"
          theme="error"
          message={tr(
            "\u67E5\u8BE2\u6D4B\u8BD5\u7ED3\u679C\u5931\u8D25\uFF1A{{value0}}",
            { value0: result.error.message },
          )}
          operation={
            <Button variant="text" onClick={() => void result.refetch()}>
              {tr("\u91CD\u8BD5\u67E5\u8BE2")}
            </Button>
          }
        />
      )}
      <Panel className="panel starter-list-panel">
        <ListToolbar
          title={tr("\u901A\u77E5\u6E20\u9053")}
          actions={
            <>
              <Button
                variant="outline"
                icon={<RefreshIcon />}
                loading={query.isFetching}
                onClick={() => void query.refetch()}
              >
                {tr("\u5237\u65B0")}
              </Button>
              {permission.write && (
                <Button icon={<AddIcon />} onClick={() => setAdding(true)}>
                  {tr("\u6DFB\u52A0\u6E20\u9053")}
                </Button>
              )}
            </>
          }
        />
        <Tabs value={tab} onChange={(value) => setTab(String(value))}>
          <Tabs.TabPanel value="active" label={tr("使用中")} />
          <Tabs.TabPanel value="archived" label={tr("已归档")} />
        </Tabs>
        <QueryState query={query}>
          <Table
            className="mobile-priority-table"
            tableLayout="fixed"
            rowKey="id"
            data={query.rows}
            hover
            empty={
              <EmptyState
                title={tr("\u8FD8\u6CA1\u6709\u901A\u77E5\u6E20\u9053")}
                description={tr(
                  "添加 Slack 或 Webhook，开始接收服务状态变化。",
                )}
                action={
                  permission.write && (
                    <Button onClick={() => setAdding(true)}>
                      {tr("\u6DFB\u52A0\u7B2C\u4E00\u4E2A\u6E20\u9053")}
                    </Button>
                  )
                }
              />
            }
            columns={[
              {
                colKey: "name",
                title: tr("\u6E20\u9053\u540D\u79F0"),
                width: 160,
                cell: ({ row }) => <strong>{row.name}</strong>,
              },
              {
                colKey: "channel",
                title: tr("\u7C7B\u578B"),
                width: 120,
                cell: ({ row }) => label(row.channel),
              },
              {
                colKey: "health_state",
                title: tr("\u5065\u5EB7\u72B6\u6001"),
                width: 120,
                cell: ({ row }) => <StatusBadge value={row.health_state} />,
              },
              {
                colKey: "enabled",
                title: tr("\u542F\u7528\u72B6\u6001"),
                width: 110,
                cell: ({ row }) => (
                  <StatusBadge value={row.enabled ? "enabled" : "disabled"} />
                ),
              },
              {
                colKey: "secret_version",
                title: tr("\u51ED\u636E"),
                width: 115,
                cell: ({ row }) => (
                  <span className="muted">
                    {tr("\u5DF2\u52A0\u5BC6 \u00B7 v")}
                    {row.secret_version}
                  </span>
                ),
              },
              {
                colKey: "updated_at",
                title: tr("\u66F4\u65B0\u65F6\u95F4"),
                width: 160,
                cell: ({ row }) => fmt(row.updated_at),
              },
              {
                colKey: "actions",
                title: tr("\u64CD\u4F5C"),
                width: 220,
                cell: ({ row }) => (
                  <div className="table-actions table-actions-nowrap">
                    {row.archived_at ? (
                      <Button
                        variant="text"
                        disabled={!permission.write}
                        onClick={() => void restore(row)}
                      >
                        {tr("恢复")}
                      </Button>
                    ) : (
                      <>
                        <Button
                          variant="text"
                          disabled={!permission.write || testing || pending}
                          onClick={() => setEditing(row)}
                        >
                          {tr("编辑")}
                        </Button>
                        <Button
                          variant="text"
                          disabled={!permission.write || testing || pending}
                          onClick={() => void test(row)}
                        >
                          {tr("发送测试")}
                        </Button>
                        <Dropdown
                          trigger="click"
                          options={[{ value: "archive", content: tr("归档") }]}
                          onClick={() => void archive(row)}
                        >
                          <Button
                            variant="text"
                            disabled={!permission.write || testing || pending}
                          >
                            {tr("更多")}
                          </Button>
                        </Dropdown>
                      </>
                    )}
                  </div>
                ),
              },
            ]}
          />
          <LoadMore query={query} />
        </QueryState>
      </Panel>
      <ChannelEditor
        visible={adding}
        onClose={() => setAdding(false)}
        onSaved={(endpoint) =>
          setNotice(
            tr(
              "\u5DF2\u6DFB\u52A0\u300C{{value0}}\u300D\uFF0C\u53EF\u4EE5\u53D1\u9001\u6D4B\u8BD5\u9A8C\u8BC1\u8FDE\u63A5\u3002",
              { value0: endpoint.name },
            ),
          )
        }
      />
      <ChannelEditor
        visible={!!editing}
        endpoint={editing}
        onClose={() => setEditing(null)}
        onSaved={(endpoint) => {
          setEditing(null);
          setNotice(tr("已更新「{{value0}}」。", { value0: endpoint.name }));
        }}
      />
    </>
  );
}

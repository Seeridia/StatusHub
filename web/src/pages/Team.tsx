import { useState } from "react";
import {
  Alert,
  Button,
  Dialog,
  Drawer,
  Input,
  Select,
  Switch,
  Table,
  Tag,
} from "tdesign-react";
import { useList, useSession, useWriter } from "../lib/hooks";
import { tr } from "../lib/i18n";
import { currentTenant, clearSession } from "../lib/api";
import { fmt } from "../lib/model";

type Entry = {
  id: string;
  name?: string;
  email?: string;
  role: string;
  enabled?: boolean;
  method?: string;
  last_used_at?: string;
  expires_at?: string;
  accepted_at?: string;
  revoked_at?: string;
};
export function Team({
  kind,
}: {
  kind: "members" | "invitations" | "service-accounts";
}) {
  const query = useList<Entry>("/" + kind);
  const { identity } = useSession();
  const write = useWriter();
  const [editing, setEditing] = useState<Entry | null>(null);
  const [role, setRole] = useState("viewer");
  const [name, setName] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState("");
  const [confirm, setConfirm] = useState<(() => Promise<void>) | null>(null);
  const [confirmationTarget,setConfirmationTarget]=useState('');
  function confirmAction(run:()=>Promise<void>,target:string){setError('');setConfirmationTarget(target);setConfirm(()=>run)}
  const roles = [
    { value: "viewer", label: tr("查看者") },
    { value: "operator", label: tr("操作员") },
    ...(identity.role === "owner"
      ? [
          { value: "admin", label: tr("管理员") },
          { value: "owner", label: tr("所有者") },
        ]
      : []),
  ];
  function allowed(row: Entry) {
    return (
      !(identity.actor_id === row.id) &&
      (identity.role === "owner" || ["viewer", "operator"].includes(row.role))
    );
  }
  async function mutate(path: string, body: unknown, method = "POST") {
    setBusy(true);
    setError("");
    try {
      const out = await write<{
        token?: string;
        invitation_token?: string;
        secret_unavailable?: boolean;
      }>(path, body, method);
      if (out.token) setResult(out.token);
      if (out.invitation_token)
        setResult(
          `${location.origin}/ui/?tenant=${encodeURIComponent(currentTenant())}#/account-flow?mode=invite&token=${out.invitation_token}`,
        );
      if (out.secret_unavailable)
        setError(
          tr("操作已完成，但凭据已无法再次显示。请重新轮换或生成邀请。"),
        );
      setEditing(null);
      setConfirm(null);
      await query.refetch();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  function edit(row?: Entry) {
    setError("");
    setEditing(row || { id: "", role: "viewer" });
    setRole(row?.role || "viewer");
    setName(row?.email || row?.name || "");
    setEnabled(row?.enabled ?? true);
  }
  function save() {
    if (!editing) return;
    const run = () =>
      mutate(
        "/" + kind + (editing.id ? "/" + editing.id : ""),
        kind === "invitations"
          ? { email: name, role }
          : kind === "members"
            ? { role, enabled }
            : { name, role, enabled },
        editing.id ? "PATCH" : "POST",
      );
    confirmAction(run,`${name} · ${role} · ${enabled?tr('已启用'):tr('已停用')}`);
  }
  return (
    <div className="team-panel">
      {error && <Alert theme="error" message={error} />}
      <div className="toolbar">
        <Button onClick={() => query.refetch()} loading={query.isFetching}>
          {tr("刷新")}
        </Button>
        {kind !== "members" && (
          <Button theme="primary" onClick={() => edit()}>
            {kind === "invitations" ? tr("创建邀请") : tr("创建服务账号")}
          </Button>
        )}
      </div>
      {kind === "members" && (
        <Alert
          message={tr(
            "管理员只能管理查看者和操作员；高权限成员由所有者管理。不能修改自己的角色或停用自己。",
          )}
        />
      )}
      <Table
        rowKey="id"
        data={query.rows}
        loading={query.isLoading}
        empty={tr("暂无记录")}
        columns={[
          {
            colKey: "name",
            title: kind === "service-accounts" ? tr("名称") : tr("邮箱"),
            minWidth: 220,
            cell: ({ row }) => (
              <span style={{ overflowWrap: "anywhere" }}>
                {row.email || row.name}
              </span>
            ),
          },
          {
            colKey: "role",
            title: tr("角色"),
            width: 120,
            cell: ({ row }) =>
              ({
                viewer: tr("查看者"),
                operator: tr("操作员"),
                admin: tr("管理员"),
                owner: tr("所有者"),
              })[row.role as string] || row.role,
          },
          {
            colKey: "enabled",
            title: tr("状态"),
            width: 140,
            cell: ({ row }) => (
              <Tag>
                {kind === "invitations"
                  ? row.accepted_at
                    ? tr("已接受")
                    : row.revoked_at
                      ? tr("已撤销")
                      : Date.parse(row.expires_at || "") < Date.now()
                        ? tr("已过期")
                        : tr("等待接受")
                  : row.enabled
                    ? tr("已启用")
                    : tr("已停用")}
              </Tag>
            ),
          },
          {
            colKey: "time",
            title:
              kind === "invitations"
                ? tr("有效期至")
                : kind === "members"
                  ? tr("登录方式")
                  : tr("最近使用"),
            minWidth: 160,
            cell: ({ row }) =>
              kind === "members"
                ? row.method === "password"
                  ? tr("邮箱密码")
                  : tr("单点登录")
                : fmt(row.expires_at || row.last_used_at),
          },
          {
            colKey: "actions",
            title: tr("操作"),
            width: 210,
            cell: ({ row }) => (
              <div>
                <Button
                  variant="text"
                  disabled={
                    !allowed(row) ||
                    (kind === "invitations" &&
                      !!(row.accepted_at || row.revoked_at))
                  }
                  onClick={() =>
                    kind === "invitations"
                      ? confirmAction(
                          () =>
                            mutate(`/invitations/${row.id}/revoke`, {}),
                          row.email||row.id,
                        )
                      : edit(row)
                  }
                >
                  {kind === "invitations" ? tr("撤销邀请") : tr("编辑")}
                </Button>
                {kind === "service-accounts" && (
                  <Button
                    variant="text"
                    disabled={!allowed(row)}
                    onClick={() =>
                      confirmAction(
                        () =>
                          mutate(`/service-accounts/${row.id}/rotate`, {}),
                        row.name||row.id,
                      )
                    }
                  >
                    {tr("轮换令牌")}
                  </Button>
                )}
              </div>
            ),
          },
        ]}
      />
      <Drawer
        visible={!!editing}
        onClose={() => setEditing(null)}
        header={tr("账号配置")}
        footer={null}
        size="480px"
      >
        <div className="team-form">
          {kind !== "members" && (
            <label>
              {kind === "invitations" ? tr("邮箱") : tr("名称")}
              <Input
                aria-label={kind === "invitations" ? tr("邮箱") : tr("名称")}
                value={name}
                onChange={setName}
                disabled={!!editing?.id}
              />
            </label>
          )}
          <label>
            {tr("角色")}
            <Select
              aria-label={tr("角色")}
              value={role}
              onChange={(v) => setRole(String(v))}
              options={roles}
            />
          </label>
          {!!editing?.id && (
            <label>
              {tr("已启用")}{" "}
              <Switch value={enabled} onChange={(v) => setEnabled(!!v)} />
            </label>
          )}
          <Button theme="primary" loading={busy} onClick={save}>
            {tr("保存")}
          </Button>
        </div>
      </Drawer>
      <Dialog
        visible={!!confirm}
        header={tr("确认账号操作")}
        body={<><p style={{overflowWrap:'anywhere'}}>{confirmationTarget}</p><p>{tr(
          "权限变更和停用会影响后续访问；轮换令牌会立即使旧令牌失效。请确认目标与角色。",
        )}</p>{error&&<Alert theme="error" message={error}/>}</>}
        confirmBtn={{ content: tr("确认"), loading: busy }}
        onConfirm={() => void confirm?.()}
        onClose={() => setConfirm(null)}
      />
      <Dialog
        visible={!!result}
        header={tr("请立即保存")}
        footer={<Button onClick={() => setResult("")}>{tr("关闭")}</Button>}
        onClose={() => setResult("")}
      >
        <Alert
          message={tr(
            "此内容仅在本次结果中显示，请安全保存。邀请链接需要由你分享给受邀者。",
          )}
        />
        <Input aria-label={tr("凭据或邀请链接")} value={result} readonly />
      </Dialog>
    </div>
  );
}
export function PersonalAccount() {
  const { identity } = useSession();
  const write = useWriter();
  const [old, setOld] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [action, setAction] = useState("");
  async function run() {
    setBusy(true);
    try {
      await write("/account/" + action, { old_password: old, password });
      clearSession();
      location.reload();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="team-form">
      <p>{identity.email || identity.actor_id}</p>
      {error && <Alert theme="error" message={error} />}
      {identity.actor_type !== "user" ? (
        <Alert message={tr("服务账号请通过服务账号管理轮换令牌。")} />
      ) : (
        <>
          {identity.issuer === "local" && (
            <>
              <label>
                {tr("原密码")}
                <Input
                  type="password"
                  autocomplete="current-password"
                  aria-label={tr("原密码")}
                  value={old}
                  onChange={setOld}
                />
              </label>
              <label>
                {tr("新密码（12–128 个字符）")}
                <Input
                  type="password"
                  autocomplete="new-password"
                  aria-label={tr("新密码（12–128 个字符）")}
                  value={password}
                  onChange={setPassword}
                />
              </label>
              <Button onClick={() => setAction("password")}>
                {tr("修改密码")}
              </Button>
            </>
          )}
          <Button onClick={() => setAction("logout-all")}>
            {tr("退出全部会话")}
          </Button>
          <Dialog
            visible={!!action}
            header={tr("确认账号操作")}
            body={<><p>{tr("操作完成后需要重新登录。")}</p>{error&&<Alert theme="error" message={error}/>}</>}
            confirmBtn={{ content: tr("确认"), loading: busy }}
            onConfirm={run}
            onClose={() => setAction("")}
          />
        </>
      )}
    </div>
  );
}

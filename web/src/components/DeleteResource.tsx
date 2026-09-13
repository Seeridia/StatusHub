import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Alert, Button } from "tdesign-react";
import { Dialog } from "../overlays";
import { useWriter } from "../lib/hooks";
import { tr } from "../lib/i18n";

export function DeleteResource({
  path,
  name,
  disabled,
  channel = false,
}: {
  path: string;
  name: string;
  disabled?: boolean;
  channel?: boolean;
}) {
  const [visible, setVisible] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const write = useWriter();
  const client = useQueryClient();
  async function remove() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await write(path, {}, "DELETE");
      setVisible(false);
      await client.invalidateQueries();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Button
        variant="text"
        theme="danger"
        disabled={disabled}
        onClick={() => {
          setError("");
          setVisible(true);
        }}
      >
        {tr("删除")}
      </Button>
      <Dialog
        header={tr("删除配置")}
        visible={visible}
        confirmBtn={{ content: tr("确认删除"), theme: "danger", loading: busy }}
        cancelBtn={{ content: tr("取消"), disabled: busy }}
        onConfirm={() => void remove()}
        onClose={() => !busy && setVisible(false)}
      >
        <p>
          {tr(
            "确认删除“{{name}}”？配置将从列表移除，历史事件和投递记录保留。已开始执行的任务可能仍会完成。",
            { name },
          )}
        </p>
        {channel && (
          <p>
            {tr("该渠道将从通知规则中移除；失去全部接收渠道的规则会自动停用。")}
          </p>
        )}
        {error && <Alert theme="error" message={error} />}
      </Dialog>
    </>
  );
}

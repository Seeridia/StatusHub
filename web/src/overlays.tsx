import { CloseIcon } from "tdesign-icons-react";
import { tr } from "./lib/i18n";
import { useEffect } from "react";
import { Dialog as TDialog, Drawer as TDrawer } from "tdesign-react";
import type { DialogProps, DrawerProps } from "tdesign-react";
let openCount = 0;
function useScrollLock(visible?: boolean) {
  useEffect(() => {
    if (!visible) return;
    openCount++;
    document.body.classList.add("overlay-open");
    return () => {
      openCount--;
      if (!openCount) document.body.classList.remove("overlay-open");
    };
  }, [visible]);
}
export function Drawer(props: DrawerProps) {
  useScrollLock(props.visible);
  return (
    <TDrawer
      closeBtn={
        <button type="button" className="overlay-close" aria-label={tr("关闭")}>
          <CloseIcon aria-hidden="true" />
        </button>
      }
      {...props}
      preventScrollThrough={false}
    />
  );
}
export function Dialog(props: DialogProps) {
  useScrollLock(props.visible);
  return (
    <TDialog
      closeBtn={
        <button type="button" className="overlay-close" aria-label={tr("关闭")}>
          <CloseIcon aria-hidden="true" />
        </button>
      }
      {...props}
      preventScrollThrough={false}
    />
  );
}

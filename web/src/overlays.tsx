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
  return <TDrawer {...props} preventScrollThrough={false} />;
}
export function Dialog(props: DialogProps) {
  useScrollLock(props.visible);
  return <TDialog {...props} preventScrollThrough={false} />;
}

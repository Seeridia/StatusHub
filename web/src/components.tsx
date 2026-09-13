import { tr } from "./lib/i18n";
import type { ReactNode } from "react";
import {
  Alert,
  Button,
  Empty,
  Form,
  Skeleton,
  Tag,
  Tooltip,
} from "tdesign-react";
import {
  CheckCircleIcon,
  ErrorCircleIcon,
  TimeIcon,
  CloudIcon,
} from "tdesign-icons-react";
import {
  collectionLabel,
  fmt,
  label,
  relative,
  stale,
  tone,
} from "./lib/model";
import type { Vendor } from "./lib/types";
import { useClock } from "./lib/hooks";
export function PageHeading({
  title,
  description,
  actions,
}: {
  title: string;
  description: string;
  actions?: ReactNode;
}) {
  return (
    <div className="page-heading">
      <div>
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      <div className="heading-actions">{actions}</div>
    </div>
  );
}
export function StatusBadge({ value }: { value?: string }) {
  const theme = tone(value);
  return (
    <Tag
      theme={theme}
      variant="light"
      icon={
        theme === "success" ? (
          <CheckCircleIcon />
        ) : theme === "danger" || theme === "warning" ? (
          <ErrorCircleIcon />
        ) : (
          <TimeIcon />
        )
      }
    >
      {label(value)}
    </Tag>
  );
}
export function VendorIdentity({
  vendor,
}: {
  vendor: Pick<Vendor, "name" | "slug">;
}) {
  return (
    <div className="vendor-identity">
      <span className="vendor-icon">
        <CloudIcon size="22px" />
      </span>
      <span>
        <strong>{vendor.name}</strong>
        <small>{vendor.slug}</small>
      </span>
    </div>
  );
}
export function Freshness({ vendor }: { vendor: Vendor }) {
  const now = useClock();
  const expired = stale(vendor, now);
  return (
    <Tooltip
      content={`${collectionLabel(expired ? "resource_overdue" : vendor.collection?.reason || "awaiting_resource_checkpoint")} · ${tr("新鲜度期限")}: ${fmt(vendor.collection?.fresh_until)}`}
    >
      <span className={expired ? "freshness warning-text" : "freshness"}>
        <TimeIcon />
        {expired
          ? tr("\u6570\u636E\u8FC7\u671F \u00B7 ")
          : vendor.collection?.state !== "fresh"
            ? `${collectionLabel(vendor.collection?.state || "unknown")} · `
            : ""}
        {relative(vendor.last_successful_at, now)}
      </span>
    </Tooltip>
  );
}
export function QueryState({
  query,
  children,
}: {
  query: {
    isPending: boolean;
    isError: boolean;
    error: Error | null;
    data?: unknown;
    refetch: () => unknown;
  };
  children: ReactNode;
}) {
  if (query.isPending)
    return (
      <div className="panel">
        <Skeleton theme="paragraph" animation="gradient" />
      </div>
    );
  return (
    <>
      {query.isError && (
        <Alert
          className="query-error"
          theme="error"
          title={tr("\u6570\u636E\u52A0\u8F7D\u5931\u8D25")}
          message={query.error?.message}
          operation={
            <Button variant="text" onClick={() => void query.refetch()}>
              {tr("\u91CD\u8BD5")}
            </Button>
          }
        />
      )}
      {(!query.isError || query.data) && children}
    </>
  );
}
export function EmptyState({
  title = tr("\u6682\u65E0\u8BB0\u5F55"),
  description = tr(
    "\u6709\u65B0\u7684\u72B6\u6001\u53D8\u5316\u65F6\u4F1A\u663E\u793A\u5728\u8FD9\u91CC\u3002",
  ),
  action,
}: {
  title?: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty-state">
      <Empty
        title={title}
        description={description}
        action={action ? <>{action}</> : undefined}
      />
    </div>
  );
}
export function LoadMore({
  query,
}: {
  query: {
    hasNextPage: boolean;
    isFetchingNextPage: boolean;
    fetchNextPage: () => unknown;
  };
}) {
  return query.hasNextPage ? (
    <div className="load-more">
      <Button
        variant="outline"
        loading={query.isFetchingNextPage}
        onClick={() => void query.fetchNextPage()}
      >
        {tr("\u52A0\u8F7D\u66F4\u591A")}
      </Button>
    </div>
  ) : null;
}
export function Field({
  title,
  children,
  hint,
}: {
  title: string;
  children: ReactNode;
  hint?: string;
}) {
  return (
    <div className="field">
      <div className="field-title">{title}</div>
      {children}
      {hint && <div className="field-hint">{hint}</div>}
    </div>
  );
}
// Controlled React state is the single source of truth; FormItem supplies layout.
export function FormField({
  children,
  ...props
}: {
  children: ReactNode;
  label: ReactNode;
  name?: string;
  help?: ReactNode;
}) {
  return (
    <Form.FormItem {...props}>
      <div className="field-control">{children}</div>
    </Form.FormItem>
  );
}

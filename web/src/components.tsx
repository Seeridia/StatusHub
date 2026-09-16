import { serviceCatalog } from "./lib/service-catalog";
import { brandIconSourcesForName } from "./lib/brands";
import { tr } from "./lib/i18n";
import {
  createContext,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { FormProps } from "tdesign-react";
import {
  Alert,
  Button,
  Card,
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
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <ListToolbar title={title} description={description} actions={actions} />
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
  const service = serviceCatalog.find((item) => {
    const hostname = new URL(item.url).hostname;
    return (
      item.name.toLowerCase() === vendor.name.toLowerCase() ||
      hostname === vendor.name.toLowerCase()
    );
  });
  const name = service?.name || vendor.name;
  const iconSources = brandIconSourcesForName(name);
  return (
    <div className="vendor-identity">
      <span className="vendor-icon">
        {iconSources.length ? (
          <RemoteBrandIcon key={name} sources={iconSources} />
        ) : (
          <CloudIcon size="22px" aria-hidden="true" />
        )}
      </span>
      <span>
        <strong translate="no">{name}</strong>
        {service && <small>{new URL(service.url).hostname}</small>}
      </span>
    </div>
  );
}

function RemoteBrandIcon({ sources }: { sources: string[] }) {
  const [sourceIndex, setSourceIndex] = useState(0);
  if (sourceIndex >= sources.length) {
    return <CloudIcon size="22px" aria-hidden="true" />;
  }
  return (
    <img
      src={sources[sourceIndex]}
      width="22"
      height="22"
      alt=""
      loading="lazy"
      decoding="async"
      referrerPolicy="no-referrer"
      onError={() => setSourceIndex((current) => current + 1)}
    />
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
type FieldErrors = Record<string, string>;
const ValidationContext = createContext<FieldErrors>({});

/** Keep controlled values in React, and render feedback through TDesign FormItem. */
export function ValidatedForm({
  validate,
  onSubmit,
  children,
  ...props
}: Omit<FormProps, "onSubmit"> & {
  validate: () => FieldErrors;
  onSubmit: () => void;
}) {
  const [submitted, setSubmitted] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const errors = submitted ? validate() : {};
  useEffect(() => {
    if (!attempt) return;
    const field = root.current?.querySelector<HTMLElement>(
      '[data-field-error="true"]',
    );
    field
      ?.querySelector<HTMLElement>(
        'input:not([disabled]), textarea:not([disabled]), button:not([disabled]), [tabindex="0"]',
      )
      ?.focus();
  }, [attempt]);
  return (
    <ValidationContext.Provider value={errors}>
      <div ref={root} className="validated-form">
        <Form
          {...props}
          onSubmit={() => {
            setSubmitted(true);
            setAttempt((value) => value + 1);
            if (!Object.keys(validate()).length) onSubmit();
          }}
        >
          {children}
        </Form>
      </div>
    </ValidationContext.Provider>
  );
}

export function FormField({
  children,
  required,
  ...props
}: {
  children: ReactNode;
  label: ReactNode;
  name?: string;
  help?: ReactNode;
  required?: boolean;
}) {
  const id = useId();
  const root = useRef<HTMLDivElement>(null);
  const errors = useContext(ValidationContext);
  const error = props.name ? errors[props.name] : undefined;
  useEffect(() => {
    const input = root.current?.querySelector(
      'input, textarea, select, [role="combobox"]',
    );
    if (!input) return;
    input.id = id;
    input.setAttribute("aria-invalid", String(!!error));
    input.setAttribute("aria-required", String(!!required));
    if (error) input.setAttribute("aria-errormessage", `${id}-error`);
    else input.removeAttribute("aria-errormessage");
  }, [id, error, required, children]);
  return (
    <Form.FormItem
      {...props}
      for={id}
      requiredMark={required}
      status={error ? "error" : undefined}
      tips={
        error ? (
          <span id={`${id}-error`} role="alert">
            {error}
          </span>
        ) : undefined
      }
    >
      <div ref={root} className="field-control" data-field-error={!!error}>
        {children}
      </div>
    </Form.FormItem>
  );
}

/** Shared Starter-style surface for resource lists and configuration sections. */
export function Panel({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <Card
      className={`resource-panel ${className}`}
      bordered={false}
      bodyStyle={{ padding: 0 }}
    >
      {children}
    </Card>
  );
}

export function ListToolbar({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="starter-list-toolbar page-heading">
      <div>
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {actions && <div className="heading-actions">{actions}</div>}
    </div>
  );
}

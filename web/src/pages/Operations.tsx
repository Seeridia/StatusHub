import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { AddIcon, RefreshIcon } from 'tdesign-icons-react';
import {
  Alert,
  Button,
  Input,
  Select,
  Table,
  Tabs,
  Timeline,
} from 'tdesign-react';
import {
  EmptyState,
  FormField,
  ListToolbar,
  LoadMore,
  Panel,
  QueryState,
  StatusBadge,
  ValidatedForm,
} from '../components';
import { DeleteResource } from '../components/DeleteResource';
import { ServiceNameInput } from '../components/ServiceNameInput';
import { api } from '../lib/api';
import { useAPI, useList, usePermissions, useWriter } from '../lib/hooks';
import { formatList, tr } from '../lib/i18n';
import { collectionLabel, fmt, label } from '../lib/model';
import type { Audit, Delivery, Source } from '../lib/types';
import { Dialog, Drawer } from '../overlays';
import { PersonalAccount, Team } from './Team';
function Deliveries() {
  const [params, setParams] = useSearchParams();
  const status = params.get('status') || '';
  const query = useList<Delivery>(
    `/deliveries${status ? `?status=${encodeURIComponent(status)}` : ''}`,
  );
  const [id, setID] = useState('');
  const [retryID, setRetryID] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const detail = useAPI<Delivery>(`/deliveries/${id}`, !!id);
  const permission = usePermissions();
  const write = useWriter();
  async function retry() {
    setBusy(true);
    setError('');
    try {
      await write(`/deliveries/${retryID}/retry`, {});
      setRetryID('');
      setNotice(
        tr('\u5DF2\u91CD\u65B0\u52A0\u5165\u6295\u9012\u961F\u5217\u3002'),
      );
      await query.refetch();
      if (id) await detail.refetch();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      {notice && (
        <Alert className="query-error" theme="success" message={notice} />
      )}
      <Panel className="panel starter-list-panel">
        <ListToolbar
          title={tr('\u6295\u9012\u8BB0\u5F55')}
          description={tr(
            '\u68C0\u67E5\u901A\u77E5\u662F\u5426\u6210\u529F\uFF0C\u4E86\u89E3\u6BCF\u4E00\u6B21\u5C1D\u8BD5\u548C\u5931\u8D25\u539F\u56E0\u3002',
          )}
          actions={
            <Button
              variant="outline"
              icon={<RefreshIcon />}
              onClick={() => void query.refetch()}
            >
              {tr('\u5237\u65B0')}
            </Button>
          }
        />
        <div className="filter-row">
          <Select
            aria-label={tr('\u6295\u9012\u72B6\u6001\u7B5B\u9009')}
            clearable
            value={status}
            placeholder={tr('\u5168\u90E8\u6295\u9012\u72B6\u6001')}
            onChange={(v) => setParams(v ? { status: String(v) } : {})}
            options={[
              'pending',
              'accepted',
              'delivered',
              'retry_wait',
              'dead_letter',
            ].map((v) => ({ value: v, label: label(v) }))}
          />
        </div>
        <QueryState query={query}>
          <Table
            tableLayout="fixed"
            rowKey="id"
            data={query.rows}
            hover
            empty={
              <EmptyState
                title={tr('\u6682\u65E0\u6295\u9012\u8BB0\u5F55')}
                description={tr(
                  '\u89C4\u5219\u5339\u914D\u5230\u4E8B\u4EF6\u540E\uFF0C\u901A\u77E5\u6295\u9012\u8FC7\u7A0B\u4F1A\u663E\u793A\u5728\u8FD9\u91CC\u3002',
                )}
              />
            }
            columns={[
              {
                colKey: 'endpoint_name',
                title: tr('\u63A5\u6536\u6E20\u9053'),
                minWidth: 180,
                cell: ({ row }) => (
                  <>
                    <strong>{row.endpoint_name}</strong>
                    <small className="cell-subtitle">
                      {label(row.channel)}
                    </small>
                  </>
                ),
              },
              {
                colKey: 'subscription_name',
                title: tr('\u901A\u77E5\u89C4\u5219'),
                minWidth: 180,
              },
              {
                colKey: 'event_kind',
                title: tr('\u4E8B\u4EF6\u7C7B\u578B'),
                width: 140,
                cell: ({ row }) => label(row.event_kind),
              },
              {
                colKey: 'status',
                title: tr('\u6295\u9012\u72B6\u6001'),
                width: 140,
                cell: ({ row }) => <StatusBadge value={row.status} />,
              },
              {
                colKey: 'attempt_count',
                title: tr('\u5C1D\u8BD5\u6B21\u6570'),
                width: 100,
              },
              {
                colKey: 'updated_at',
                title: tr('\u66F4\u65B0\u65F6\u95F4'),
                minWidth: 180,
                cell: ({ row }) => fmt(row.updated_at),
              },
              {
                colKey: 'actions',
                fixed: 'right',
                title: tr('\u64CD\u4F5C'),
                width: 150,
                cell: ({ row }) => (
                  <>
                    <Button variant="text" onClick={() => setID(row.id)}>
                      {tr('\u8BE6\u60C5')}
                    </Button>
                    {permission.write && row.status === 'dead_letter' && (
                      <Button variant="text" onClick={() => setRetryID(row.id)}>
                        {tr('\u91CD\u8BD5')}
                      </Button>
                    )}
                  </>
                ),
              },
            ]}
          />
          <LoadMore query={query} />
        </QueryState>
      </Panel>
      <Drawer
        header={tr('\u6295\u9012\u8BE6\u60C5')}
        visible={!!id}
        onClose={() => setID('')}
        size="600px"
        footer={null}
      >
        <QueryState query={detail}>
          {detail.data && (
            <div className="drawer-body">
              <h2>{detail.data.endpoint_name}</h2>
              <StatusBadge value={detail.data.status} />
              <div className="detail-line">
                <span>{tr('\u901A\u77E5\u89C4\u5219')}</span>
                {detail.data.subscription_name}
              </div>
              <div className="detail-line">
                <span>{tr('\u4E8B\u4EF6\u7C7B\u578B')}</span>
                {label(detail.data.event_kind)}
              </div>
              {detail.data.last_error_summary && (
                <Alert theme="error" message={detail.data.last_error_summary} />
              )}
              <h3>{tr('\u5C1D\u8BD5\u8BB0\u5F55')}</h3>
              {detail.data.attempts?.length ? (
                <Timeline className="incident-timeline">
                  {detail.data.attempts.map((a) => (
                    <Timeline.Item key={a.id}>
                      <time
                        className="incident-timeline-time"
                        dateTime={a.started_at}
                      >
                        {fmt(a.started_at)}
                      </time>
                      <StatusBadge value={a.status} />
                      <p>
                        {a.http_status
                          ? `HTTP ${a.http_status}`
                          : tr('\u6682\u65E0\u63A5\u6536\u7AEF\u54CD\u5E94')}
                      </p>
                      {a.error_summary && (
                        <p className="timeline-body">{a.error_summary}</p>
                      )}
                    </Timeline.Item>
                  ))}
                </Timeline>
              ) : (
                <EmptyState
                  title={tr('\u5C1A\u672A\u5F00\u59CB\u6295\u9012')}
                  description={tr(
                    '\u5DE5\u4F5C\u8FDB\u7A0B\u5904\u7406\u540E\u4F1A\u663E\u793A\u5C1D\u8BD5\u8BB0\u5F55\u3002',
                  )}
                />
              )}
            </div>
          )}
        </QueryState>
      </Drawer>
      <Dialog
        header={tr('\u91CD\u65B0\u6295\u9012\u901A\u77E5')}
        visible={!!retryID}
        confirmBtn={{
          content: tr('\u52A0\u5165\u91CD\u8BD5\u961F\u5217'),
          loading: busy,
        }}
        onConfirm={() => void retry()}
        onClose={() => !busy && setRetryID('')}
      >
        <p>
          {tr(
            '\u6B64\u64CD\u4F5C\u4F1A\u91CD\u65B0\u5411\u539F\u6E20\u9053\u6295\u9012\u901A\u77E5\u3002\u82E5\u63A5\u6536\u7AEF\u6B64\u524D\u5DF2\u6536\u5230\u4F46\u672A\u786E\u8BA4\uFF0C\u53EF\u80FD\u6536\u5230\u91CD\u590D\u901A\u77E5\u3002',
          )}
        </p>
        {error && <Alert theme="error" message={error} />}
      </Dialog>
    </>
  );
}
function Sources() {
  const query = useList<Source>('/sources');
  const permission = usePermissions();
  const write = useWriter();
  const [adding, setAdding] = useState(false);
  const [scheduleID, setScheduleID] = useState<string | null>(null);
  const schedule = query.rows.find((row) => row.id === scheduleID);
  const [url, setURL] = useState('');
  const [serviceName, setServiceName] = useState('');
  const [detected, setDetected] = useState<{
    vendor: {
      name: string;
      new: boolean;
    };
    canonical_url: string;
    capabilities: {
      engine: string;
      endpoints: Record<string, unknown>;
    };
    existing_source?: Source;
  } | null>(null);
  async function detect() {
    if (!url.trim()) {
      setError(tr('\u8BF7\u586B\u5199\u72B6\u6001\u9875\u5730\u5740\u3002'));
      return;
    }
    setBusy(true);
    setError('');
    setDetected(null);
    try {
      setDetected(
        await api('/sources:probe', {
          method: 'POST',
          body: JSON.stringify({
            url: url.trim(),
            display_name: serviceName.trim(),
          }),
        }),
      );
    } catch (err) {
      setError(
        tr(
          '\u6682\u65F6\u65E0\u6CD5\u63A5\u5165\u8BE5\u5730\u5740\u3002{{value0}}',
          { value0: (err as Error).message },
        ),
      );
    } finally {
      setBusy(false);
    }
  }
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function save() {
    if (!detected || !url.trim()) {
      setError(
        tr('\u8BF7\u5148\u68C0\u6D4B\u72B6\u6001\u9875\u5730\u5740\u3002'),
      );
      return;
    }
    setBusy(true);
    setError('');
    try {
      await write('/sources', {
        url: url.trim(),
        display_name: serviceName.trim(),
      });
      setAdding(false);
      setURL('');
      setServiceName('');
      setDetected(null);
      await query.refetch();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <div className="section-head">
        <div>
          <h2>{tr('\u6570\u636E\u6E90')}</h2>
          <p>
            {tr(
              '\u68C0\u67E5\u91C7\u96C6\u5065\u5EB7\uFF0C\u7BA1\u7406\u79DF\u6237\u79C1\u6709\u72B6\u6001\u9875\u3002',
            )}
          </p>
        </div>
        <div className="heading-actions">
          <Button
            variant="outline"
            icon={<RefreshIcon />}
            onClick={() => query.refetch()}
          >
            {tr('刷新')}
          </Button>
          {permission.write && (
            <Button
              icon={<AddIcon />}
              onClick={() => {
                setError('');
                setAdding(true);
              }}
            >
              {tr('\u6DFB\u52A0\u6570\u636E\u6E90')}
            </Button>
          )}
        </div>
      </div>
      <details className="collection-help">
        <summary>{tr('采集频率说明')}</summary>
        <Alert
          theme="info"
          message={tr(
            '采集按资源独立调度。稳定期通常为 4–5 分钟，事件活跃期为 60–90 秒；维护资源为 5–15 分钟，上游缓存和退避可能延长等待。发现新事件还需叠加上游发布与缓存时间。查看采集计划了解实际安排。',
          )}
        />
      </details>
      <QueryState query={query}>
        <Table
          tableLayout="fixed"
          rowKey="id"
          data={query.rows}
          hover
          empty={<EmptyState title={tr('\u6682\u65E0\u6570\u636E\u6E90')} />}
          columns={[
            { colKey: 'vendor_name', title: tr("服务"), width: 140 },
            {
              colKey: 'canonical_url',
              title: tr('\u72B6\u6001\u9875\u5730\u5740'),
              minWidth: 260,
              ellipsis: true,
            },
            {
              colKey: 'health_state',
              title: tr('\u91C7\u96C6\u5065\u5EB7'),
              width: 135,
              cell: ({ row }) => <StatusBadge value={row.health_state} />,
            },
            {
              colKey: 'last_success_at',
              title: tr('\u6700\u8FD1\u6210\u529F\u91C7\u96C6'),
              minWidth: 180,
              cell: ({ row }) => fmt(row.last_success_at),
            },
            {
              colKey: 'collection',
              title: tr('采集计划'),
              minWidth: 220,
              cell: ({ row }) => (
                <>
                  <Button variant="text" onClick={() => setScheduleID(row.id)}>
                    {collectionLabel(row.collection?.state || 'unknown')}
                  </Button>
                  <small className="cell-subtitle">
                    {tr('下次采集')}:{' '}
                    {fmt(row.enabled ? row.next_poll_at : undefined)}
                  </small>
                </>
              ),
            },
            {
              colKey: 'delete',
              fixed: 'right',
              title: tr('操作'),
              width: 120,
              cell: ({ row }) =>
                row.tenant_id ? (
                  <DeleteResource
                    path={`/sources/${row.id}`}
                    name={row.vendor_name}
                    disabled={!permission.write}
                  />
                ) : (
                  <span className="muted">{tr('平台管理')}</span>
                ),
            },
          ]}
        />
        <LoadMore query={query} />
      </QueryState>
      <Drawer
        header={tr('采集计划')}
        visible={!!schedule}
        onClose={() => setScheduleID(null)}
        footer={null}
        size="680px"
      >
        {schedule && (
          <div className="drawer-body">
            <h3>{schedule.vendor_name}</h3>
            <p>
              {collectionLabel(
                schedule.collection?.reason || 'awaiting_resource_checkpoint',
              )}
            </p>
            <dl>
              {schedule.failure_streak > 0 && (
                <>
                  <dt>{tr('最近失败原因')}</dt>
                  <dd>
                    {collectionLabel(
                      schedule.collection?.failure_code || 'unclassified',
                    )}
                  </dd>
                </>
              )}
              <dt>{tr('调度阶段')}</dt>
              <dd>{collectionLabel(schedule.collection?.mode)}</dd>
              <dt>{tr('最近尝试')}</dt>
              <dd>{fmt(schedule.last_attempt_at)}</dd>
              <dt>{tr('最近成功采集')}</dt>
              <dd>{fmt(schedule.last_success_at)}</dd>
              <dt>{tr('下次采集')}</dt>
              <dd>
                {fmt(schedule.enabled ? schedule.next_poll_at : undefined)}
              </dd>
              <dt>{tr('连续失败次数')}</dt>
              <dd>{schedule.failure_streak}</dd>
            </dl>
            <Alert
              theme="info"
              message={tr(
                '新鲜度期限是资源成功采集后计划的下次时间，加上 2 分钟执行宽限。失败重试不会延长此期限。旧检查点会在各资源下次成功采集后补齐；尚无记录不代表服务故障。',
              )}
            />
            <details>
              <summary>{tr('技术详情')}</summary>
              <p>
                {tr('适配器：')} {schedule.adapter_name} ·{' '}
                {schedule.adapter_version}
              </p>
            </details>
            <Table
              tableLayout="fixed"
              rowKey="kind"
              data={schedule.collection?.resources || []}
              empty={<EmptyState title={tr('等待资源检查点')} />}
              columns={[
                {
                  colKey: 'kind',
                  title: tr('资源类型'),
                  minWidth: 150,
                  cell: ({ row }) => collectionLabel(row.kind),
                },
                {
                  colKey: 'last_success_at',
                  title: tr('最近成功采集'),
                  minWidth: 190,
                  cell: ({ row }) => fmt(row.last_success_at),
                },
                {
                  colKey: 'next_poll_at',
                  title: tr('资源计划时间'),
                  minWidth: 190,
                  cell: ({ row }) => fmt(row.next_poll_at),
                },
                {
                  colKey: 'fresh_until',
                  title: tr('新鲜度期限'),
                  minWidth: 190,
                  cell: ({ row }) => fmt(row.fresh_until),
                },
                {
                  colKey: 'schedule_reason',
                  title: tr('调度依据'),
                  minWidth: 150,
                  cell: ({ row }) => collectionLabel(row.schedule_reason),
                },
              ]}
            />
            <p className="muted">
              {tr(
                '资源计划时间属于最近成功检查点；失败退避时实际重试不会早于来源的下次采集时间。',
              )}
            </p>
          </div>
        )}
      </Drawer>
      <Drawer
        header={tr('\u6DFB\u52A0\u6570\u636E\u6E90')}
        visible={adding}
        onClose={() => !busy && setAdding(false)}
        footer={null}
        size="520px"
      >
        <div className="drawer-body">
          <ValidatedForm
            key={String(adding)}
            validate={(): Record<string, string> =>
              url.trim() ? {} : { url: tr('请填写状态页地址。') }
            }
            labelAlign="top"
            layout="vertical"
            onSubmit={() => void (detected ? save() : detect())}
          >
            <FormField label={tr('服务名称')} name="serviceName">
              <ServiceNameInput
                value={serviceName}
                disabled={busy}
                onChange={(name, suggestedURL) => {
                  setServiceName(name);
                  setURL(suggestedURL ?? '');
                  setDetected(null);
                  setError('');
                }}
              />
            </FormField>
            <p className="muted">
              {tr(
                '选择服务可自动填写状态页；未收录的服务请自行填写地址。接入前仍会检测是否支持。',
              )}
            </p>
            <FormField
              label={tr('\u72B6\u6001\u9875\u5730\u5740')}
              name="url"
              required
            >
              <Input
                aria-label={tr('\u72B6\u6001\u9875\u5730\u5740')}
                value={url}
                disabled={busy}
                onChange={(value) => {
                  setURL(value);
                  setDetected(null);
                  setError('');
                }}
                placeholder="https://status.openai.com/"
              />
            </FormField>
            {error && <Alert theme="error" message={error} />}
            {detected && (
              <>
                <Alert
                  theme="success"
                  message={
                    detected.existing_source
                      ? tr(
                          '\u8BE5\u72B6\u6001\u9875\u5DF2\u63A5\u5165\uFF0C\u53EF\u76F4\u63A5\u67E5\u770B\uFF0C\u65E0\u9700\u91CD\u590D\u6DFB\u52A0\u3002',
                        )
                      : tr(
                          '\u68C0\u6D4B\u6210\u529F\uFF0C\u53EF\u4EE5\u63A5\u5165\u3002',
                        )
                  }
                />
                <div className="detail-line">
                  <span>
                    {detected.vendor.new
                      ? tr('\u7AD9\u70B9\u540D\u79F0')
                      : tr("识别服务")}
                  </span>
                  {detected.vendor.name}
                </div>
                <div className="detail-line">
                  <span>{tr('\u72B6\u6001\u9875')}</span>
                  {detected.canonical_url}
                </div>
                <p>
                  {tr('\u53EF\u91C7\u96C6\uFF1A')}
                  {formatList(
                    Object.keys(detected.capabilities.endpoints).map(
                      (key) =>
                        ({
                          summary: tr('\u72B6\u6001\u6982\u89C8'),
                          components: tr('\u670D\u52A1\u7EC4\u4EF6'),
                          incidents: tr('\u4E8B\u4EF6\u8BB0\u5F55'),
                          unresolved_incidents: tr(
                            '\u672A\u7ED3\u675F\u4E8B\u4EF6',
                          ),
                          scheduled_maintenances: tr(
                            '\u8BA1\u5212\u7EF4\u62A4',
                          ),
                          history: tr('\u5386\u53F2\u8BB0\u5F55'),
                        })[key] || key,
                    ),
                  )}
                </p>
                <details>
                  <summary>{tr('\u6280\u672F\u8BE6\u60C5')}</summary>
                  <p>
                    {tr('\u9002\u914D\u5668\uFF1A')}
                    {detected.capabilities.engine}
                  </p>
                </details>
              </>
            )}
            <div className="form-actions">
              {detected?.existing_source ? (
                <Button onClick={() => setAdding(false)}>
                  {tr('\u5B8C\u6210')}
                </Button>
              ) : (
                <Button type="submit" loading={busy}>
                  {detected
                    ? tr('\u786E\u8BA4\u63A5\u5165')
                    : tr('\u68C0\u6D4B\u72B6\u6001\u9875')}
                </Button>
              )}
            </div>
          </ValidatedForm>
        </div>
      </Drawer>
    </>
  );
}
function AuditLog() {
  const query = useList<Audit>('/audit-events');
  return (
    <>
      <div className="section-head">
        <div>
          <h2>{tr('\u5BA1\u8BA1\u8BB0\u5F55')}</h2>
          <p>
            {tr(
              '\u8FFD\u8E2A\u5DE5\u4F5C\u533A\u914D\u7F6E\u53D8\u66F4\u53CA\u64CD\u4F5C\u8005\u3002',
            )}
          </p>
        </div>
      </div>
      <QueryState query={query}>
        <Table
          tableLayout="fixed"
          rowKey="sequence"
          data={query.rows}
          empty={
            <EmptyState title={tr('\u6682\u65E0\u5BA1\u8BA1\u8BB0\u5F55')} />
          }
          columns={[
            { colKey: 'sequence', title: tr('\u5E8F\u53F7'), width: 80 },
            { colKey: 'action', title: tr('\u64CD\u4F5C'), minWidth: 180 },
            {
              colKey: 'resource_type',
              title: tr('\u8D44\u6E90\u7C7B\u578B'),
              minWidth: 130,
            },
            {
              colKey: 'resource_id',
              title: tr('\u8D44\u6E90\u6807\u8BC6'),
              minWidth: 230,
              ellipsis: true,
            },
            {
              colKey: 'actor_type',
              title: tr('\u64CD\u4F5C\u8005'),
              minWidth: 220,
              cell: ({ row }) => (
                <>
                  {row.actor_type}
                  <small className="cell-subtitle">{row.actor_id}</small>
                </>
              ),
            },
            {
              colKey: 'occurred_at',
              title: tr('\u64CD\u4F5C\u65F6\u95F4'),
              minWidth: 180,
              cell: ({ row }) => fmt(row.occurred_at),
            },
          ]}
        />
        <LoadMore query={query} />
      </QueryState>
    </>
  );
}
export default function Operations({
  view,
}: {
  view: 'deliveries' | 'sources' | 'settings';
}) {
  const permission = usePermissions();
  const [tab, setTab] = useState('account');
  if (view === 'deliveries') return <Deliveries />;
  if (view === 'sources') return <Panel><Sources /></Panel>;
  return (
    <>
      <Panel className="panel settings-panel">
        <Tabs value={tab} onChange={(v) => setTab(String(v))}>
          <Tabs.TabPanel value="account" label={tr('个人账号')} />
          {permission.admin && (
            <Tabs.TabPanel value="members" label={tr('成员')} />
          )}
          {permission.admin && (
            <Tabs.TabPanel value="invitations" label={tr('邀请')} />
          )}
          {permission.admin && (
            <Tabs.TabPanel value="service-accounts" label={tr('服务账号')} />
          )}
          {permission.admin && (
            <Tabs.TabPanel
              value="audit"
              label={tr('\u5BA1\u8BA1\u8BB0\u5F55')}
            />
          )}
        </Tabs>
        <div className="settings-content">
          {tab === 'account' ? (
            <PersonalAccount />
          ) : permission.admin &&
            (tab === 'members' ||
              tab === 'invitations' ||
              tab === 'service-accounts') ? (
            <Team key={tab} kind={tab} />
          ) : tab === 'audit' && permission.admin ? (
            <AuditLog />
          ) : (
            <PersonalAccount />
          )}
        </div>
      </Panel>
    </>
  );
}

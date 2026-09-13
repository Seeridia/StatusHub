import { currentLanguage, tr } from "./lib/i18n";
import React, { Suspense } from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { HashRouter } from "react-router-dom";
import { ConfigProvider } from "tdesign-react";
import enUS from "tdesign-react/es/locale/en_US";
import zhCN from "tdesign-react/es/locale/zh_CN";
import "tdesign-react/es/style/index.css";
import "./styles.css";
import App from "./App";
const client = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30000,
      retry: (count, error) =>
        !(
          "status" in error && [401, 403, 404].includes(Number(error.status))
        ) && count < 1,
    },
    mutations: { retry: false },
  },
});
class ErrorBoundary extends React.Component<
  {
    children: React.ReactNode;
  },
  {
    error: boolean;
  }
> {
  state = { error: false };
  static getDerivedStateFromError() {
    return { error: true };
  }
  render() {
    return this.state.error ? (
      <div className="fatal-error">
        <h1>{tr("\u9875\u9762\u6682\u65F6\u65E0\u6CD5\u663E\u793A")}</h1>
        <p>
          {tr(
            "\u8BF7\u5237\u65B0\u91CD\u8BD5\u3002\u5982\u679C\u95EE\u9898\u6301\u7EED\uFF0C\u8BF7\u8054\u7CFB\u7BA1\u7406\u5458\u3002",
          )}
        </p>
        <button onClick={() => location.reload()}>
          {tr("\u5237\u65B0\u9875\u9762")}
        </button>
      </div>
    ) : (
      this.props.children
    );
  }
}
const language = currentLanguage();
document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
document.title = `StatusMon · ${tr("厂商状态工作台")}`;

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ErrorBoundary>
      <ConfigProvider globalConfig={language === "zh" ? zhCN : enUS}>
        <QueryClientProvider client={client}>
          <HashRouter>
            <Suspense
              fallback={
                <div className="boot">
                  {tr("\u6B63\u5728\u52A0\u8F7D\u5DE5\u4F5C\u53F0\u2026")}
                </div>
              }
            >
              <App />
            </Suspense>
          </HashRouter>
        </QueryClientProvider>
      </ConfigProvider>
    </ErrorBoundary>
  </React.StrictMode>,
);

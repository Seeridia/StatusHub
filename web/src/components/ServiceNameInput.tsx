import { useEffect, useId, useRef, useState } from "react";
import { SelectInput } from "tdesign-react";
import { SearchIcon } from "tdesign-icons-react";
import { filterServices, type ServiceSuggestion } from "../lib/service-catalog";
import { tr } from "../lib/i18n";

export function ServiceNameInput({
  value,
  disabled,
  onChange,
}: {
  value: string;
  disabled?: boolean;
  onChange: (name: string, url?: string) => void;
}) {
  const id = useId();
  const container = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const matches = filterServices(value);
  function choose(service: ServiceSuggestion) {
    onChange(service.name, service.url);
    setOpen(false);
    setActive(-1);
  }
  useEffect(() => {
    const input = container.current?.querySelector("input");
    if (!input) return;
    const attributes = {
      role: "combobox",
      "aria-label": tr("服务名称"),
      "aria-autocomplete": "list",
      "aria-expanded": String(open),
      "aria-controls": id,
    };
    Object.entries(attributes).forEach(([key, val]) =>
      input.setAttribute(key, val),
    );
    if (open && active >= 0)
      input.setAttribute("aria-activedescendant", `${id}-${active}`);
    else input.removeAttribute("aria-activedescendant");
  });
  return (
    <div ref={container}>
      <SelectInput
        allowInput
        clearable
        disabled={disabled}
        value={value}
        inputValue={value}
        prefixIcon={<SearchIcon />}
        placeholder={tr("搜索或输入服务名称")}
        popupVisible={open && !disabled}
        onPopupVisibleChange={setOpen}
        onInputChange={(name) => {
          onChange(name);
          setActive(-1);
          setOpen(true);
        }}
        onClear={() => {
          onChange("");
          setActive(-1);
        }}
        inputProps={{
          maxlength: 120,
          onKeydown: (_, { e }) => {
            if (e.nativeEvent.isComposing) return;
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              setOpen(true);
              const next = matches.length
                ? active === -1
                  ? e.key === "ArrowDown"
                    ? 0
                    : matches.length - 1
                  : (active +
                      (e.key === "ArrowDown" ? 1 : -1) +
                      matches.length) %
                    matches.length
                : -1;
              setActive(next);
              document
                .getElementById(`${id}-${next}`)
                ?.scrollIntoView({ block: "nearest" });
            } else if (e.key === "Enter" && open) {
              e.preventDefault();
              if (active >= 0 && matches[active]) choose(matches[active]);
              else setOpen(false);
            } else if (e.key === "Escape") {
              e.preventDefault();
              e.stopPropagation();
              setOpen(false);
            } else if (e.key === "Tab") setOpen(false);
          },
        }}
        panel={
          <div
            id={id}
            role="listbox"
            aria-label={tr("服务名称")}
            className="service-suggestions"
          >
            {matches.map((service, index) => (
              <div
                key={service.url}
                id={`${id}-${index}`}
                role="option"
                aria-selected={index === active}
                className={`service-suggestion${index === active ? " is-active" : ""}`}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => choose(service)}
              >
                <span>{service.name}</span>
                <small>{new URL(service.url).hostname}</small>
              </div>
            ))}
            {!matches.length && (
              <p className="service-suggestions-empty">
                {tr("未找到服务，请在下方填写状态页地址。")}
              </p>
            )}
          </div>
        }
      />
    </div>
  );
}

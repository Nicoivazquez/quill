import { QuillTextLogo } from "./QuillTextLogo";

export function QuillLogo({ className = "", onClick }: { className?: string; onClick?: () => void }) {
  const clickable = typeof onClick === "function";

  return (
    <div
      className={`${className} flex items-center gap-2 ${clickable ? "cursor-pointer hover:opacity-90 focus:opacity-90 outline-none" : ""}`}
      role={clickable ? ("button" as const) : undefined}
      tabIndex={clickable ? 0 : undefined}
      onClick={onClick}
      onKeyDown={(e) => {
        if (!clickable) return;
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick?.();
        }
      }}
    >
      <QuillIcon className="text-[1.35rem] leading-none sm:text-[1.5rem] select-none" />
      <QuillTextLogo className="hidden sm:block" />
    </div>
  );
}

export function QuillIcon({ className = "" }: { className?: string }) {
  return (
    <span className={className} aria-hidden="true">
      🪶
    </span>
  );
}

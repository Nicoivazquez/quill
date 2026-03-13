export function QuillTextLogo({ className = "" }: { className?: string }) {
  return (
    <span
      className={`quill-wordmark inline-block text-[1.55rem] leading-[1.1] select-none ${className}`}
      aria-label="Quill"
    >
      Quill{"\u2009"}
    </span>
  );
}

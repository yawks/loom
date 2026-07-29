import { MessageCircle, TestTube } from "lucide-react";
import googleChatIcon from "@/assets/google-chat.svg";
import googleMessagesIcon from "@/assets/google-messages.svg";

interface ProtocolIconProps {
  protocol: string;
  className?: string;
  size?: number;
}

export function ProtocolIcon({ protocol, className = "", size = 20 }: ProtocolIconProps) {
  const protocolLower = protocol.toLowerCase();

  if (protocolLower === "whatsapp") {
    return (
      <svg
        viewBox="0 0 24 24"
        fill="currentColor"
        className={className}
        width={size}
        height={size}
        style={{ color: "#25D366" }}
      >
        <path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413Z" />
      </svg>
    );
  }

  if (protocolLower === "slack") {
    return (
      <svg
        viewBox="0 0 24 24"
        className={className}
        width={size}
        height={size}
      >
        <path fill="#36C5F0" d="M5.042 15.165a2.528 2.528 0 0 1-2.52 2.523A2.528 2.528 0 0 1 0 15.165a2.527 2.527 0 0 1 2.522-2.52h2.52v2.52zM6.313 15.165a2.527 2.527 0 0 1 2.521-2.52 2.527 2.527 0 0 1 2.521 2.52v6.313A2.528 2.528 0 0 1 8.834 24a2.528 2.528 0 0 1-2.521-2.522v-6.313z" />
        <path fill="#2EB67D" d="M8.834 5.042a2.528 2.528 0 0 1-2.521-2.52A2.528 2.528 0 0 1 8.834 0a2.528 2.528 0 0 1 2.521 2.522v2.52H8.834zM8.834 6.313a2.528 2.528 0 0 1 2.521 2.521 2.528 2.528 0 0 1-2.521 2.521H2.522A2.528 2.528 0 0 1 0 8.834a2.528 2.528 0 0 1 2.522-2.521h6.312z" />
        <path fill="#ECB22E" d="M18.956 8.834a2.528 2.528 0 0 1 2.522-2.521A2.528 2.528 0 0 1 24 8.834a2.528 2.528 0 0 1-2.522 2.521h-2.522V8.834zM17.688 8.834a2.528 2.528 0 0 1-2.523 2.521 2.527 2.527 0 0 1-2.52-2.521V2.522A2.527 2.527 0 0 1 15.165 0a2.528 2.528 0 0 1 2.523 2.522v6.312z" />
        <path fill="#E01E5A" d="M15.165 18.956a2.528 2.528 0 0 1 2.523 2.522A2.528 2.528 0 0 1 15.165 24a2.527 2.527 0 0 1-2.52-2.522v-2.522h2.52zM15.165 17.688a2.527 2.527 0 0 1-2.52-2.523 2.526 2.526 0 0 1 2.52-2.52h6.313A2.527 2.527 0 0 1 24 15.165a2.528 2.528 0 0 1-2.522 2.523h-6.313z" />
      </svg>
    );
  }

  if (protocolLower === "googlemessages") {
    return <img src={googleMessagesIcon} alt="Google Messages" className={className} width={size} height={size} />;
  }

  if (protocolLower === "googlechat") {
    return <img src={googleChatIcon} alt="Google Chat" className={className} width={size} height={size} />;
  }

  if (protocolLower === "teams") {
    return (
      <svg viewBox="0 0 24 24" className={className} width={size} height={size} aria-label="Microsoft Teams">
        <path fill="#5059C9" d="M9.5 6.5h8A2.5 2.5 0 0 1 20 9v7.5a4.5 4.5 0 0 1-9 0V8a1.5 1.5 0 0 0-1.5-1.5Z" />
        <circle cx="16.5" cy="3.5" r="2.5" fill="#7B83EB" />
        <path fill="#7B83EB" d="M13 8h8.5A2.5 2.5 0 0 1 24 10.5V16a4 4 0 0 1-4 4h-.7c.45-1.05.7-2.24.7-3.5V9c0-.35-.04-.68-.13-1H13Z" />
        <rect x="0" y="5" width="14" height="14" rx="2" fill="#4B53BC" />
        <path fill="white" d="M3 8h8v2H8v6H6v-6H3Z" />
      </svg>
    );
  }

  if (protocolLower === "mock") {
    return (
      <TestTube
        className={className}
        size={size}
        style={{ color: "#6366F1" }}
      />
    );
  }

  // Default icon for unknown protocols
  return <MessageCircle className={className} size={size} />;
}

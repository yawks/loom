import { useState, useRef, useEffect } from "react";
import { Play, Pause } from "lucide-react";
import { GetAttachmentData, MarkMessageAsPlayed } from "../../wailsjs/go/main/App";

interface VoiceMessageProps {
    attachment: {
        url: string;
        duration?: number; // Duration in seconds
        fileName: string;
    };
    conversationID: string;
    messageID: string;
    isFromMe: boolean;
    layout?: "bubble" | "irc";
}

export function VoiceMessage({
    attachment,
    conversationID,
    messageID,
    isFromMe,
    layout = "bubble"
}: VoiceMessageProps) {
    const [isPlaying, setIsPlaying] = useState(false);
    const [progress, setProgress] = useState(0);
    const [duration, setDuration] = useState(attachment.duration || 0);
    const [audioUrl, setAudioUrl] = useState<string | null>(null);
    const [playbackRate, setPlaybackRate] = useState(1);
    const [hasPlayedAndMarked, setHasPlayedAndMarked] = useState(false);
    const [waveform, setWaveform] = useState<number[]>([]);

    const audioRef = useRef<HTMLAudioElement | null>(null);

    // Convert Float32Array to WAV Blob
    const encodeWAV = (samples: Float32Array, sampleRate: number) => {
        const buffer = new ArrayBuffer(44 + samples.length * 2);
        const view = new DataView(buffer);

        // RIFF chunk descriptor
        writeString(view, 0, 'RIFF');
        view.setUint32(4, 36 + samples.length * 2, true);
        writeString(view, 8, 'WAVE');

        // fmt sub-chunk
        writeString(view, 12, 'fmt ');
        view.setUint32(16, 16, true);
        view.setUint16(20, 1, true); // PCM (integer)
        view.setUint16(22, 1, true); // Mono
        view.setUint32(24, sampleRate, true);
        view.setUint32(28, sampleRate * 2, true);
        view.setUint16(32, 2, true); // Block align
        view.setUint16(34, 16, true); // Bits per sample

        // data sub-chunk
        writeString(view, 36, 'data');
        view.setUint32(40, samples.length * 2, true);

        // Write sample data
        floatTo16BitPCM(view, 44, samples);

        return new Blob([view], { type: 'audio/wav' });
    };

    const writeString = (view: DataView, offset: number, string: string) => {
        for (let i = 0; i < string.length; i++) {
            view.setUint8(offset + i, string.charCodeAt(i));
        }
    };

    const floatTo16BitPCM = (output: DataView, offset: number, input: Float32Array) => {
        for (let i = 0; i < input.length; i++, offset += 2) {
            const s = Math.max(-1, Math.min(1, input[i]));
            output.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7FFF, true);
        }
    };

    // Calculate RMS based waveform
    const calculateWaveform = (samples: Float32Array, bars: number = 40) => {
        const points: number[] = [];
        const blockSize = Math.floor(samples.length / bars);

        for (let i = 0; i < bars; i++) {
            const start = i * blockSize;
            let sum = 0;
            for (let j = 0; j < blockSize; j++) {
                sum += Math.abs(samples[start + j]);
            }
            points.push(sum / blockSize);
        }

        // Normalize
        const max = Math.max(...points, 0.001);
        return points.map(p => p / max);
    }

    // Load audio data
    useEffect(() => {
        let active = true;
        const loadAudio = async () => {
            if (!attachment.url) return;
            try {
                const data = await GetAttachmentData(attachment.url);
                if (!active) return;

                // Check if it's OGG/Opus (common on WhatsApp)
                // We check:
                // 1. MIME type info from backend
                // 2. File extension
                // 3. Magic bytes (OggS) in the header

                const base64Content = data.split(',')[1];
                const binaryString = window.atob(base64Content);
                const isOggMagic = binaryString.startsWith("OggS");

                console.log("[VoiceMessage] Loading:", {
                    url: attachment.url,
                    mime: data.split(';')[0],
                    isOggMagic,
                    extension: attachment.fileName.split('.').pop()
                });

                // Check headers, extension, or magic bytes
                if (data.startsWith("data:audio/ogg") || attachment.fileName.endsWith(".ogg") || isOggMagic) {
                    try {
                        console.log("[VoiceMessage] Attempting OGG decoding...");
                        // Dynamic import to avoid SSR issues if any (though this is SPA)
                        const { OggOpusDecoder } = await import("ogg-opus-decoder");

                        const len = binaryString.length;
                        const bytes = new Uint8Array(len);
                        for (let i = 0; i < len; i++) {
                            bytes[i] = binaryString.charCodeAt(i);
                        }

                        const decoder = new OggOpusDecoder();
                        await decoder.ready;
                        const { channelData, sampleRate } = await decoder.decode(bytes);

                        console.log("[VoiceMessage] Decoded OGG successfully", { sampleRate, channels: channelData.length });

                        // Generate waveform data from the first channel
                        if (channelData.length > 0) {
                            setWaveform(calculateWaveform(channelData[0]));
                        }

                        // WhatsApp voice notes are usually mono (channelData[0])
                        // If stereo, we'd need to interleave, but let's assume mono/take first channel for now
                        const wavBlob = encodeWAV(channelData[0], sampleRate);
                        const url = URL.createObjectURL(wavBlob);

                        setAudioUrl(url);
                        return;
                    } catch (decodeErr) {
                        console.warn("[VoiceMessage] Failed to decode OGG, falling back to original source:", decodeErr);
                    }
                }

                // Fallback for supported formats (MP3/M4A) or opaque failures
                console.log("[VoiceMessage] Using native playback fallback");
                setAudioUrl(data);
                // For fallback, we might not have a waveform unless we decode it via WebAudio
                // which is async and might not work for all formats.
                // We'll show a simple progress bar if waveform is empty.
            } catch (err) {
                console.error("Failed to load voice message:", err);
            }
        };
        loadAudio();
        return () => {
            active = false;
        };
    }, [attachment.url, attachment.fileName]);

    // Handle playback rate
    useEffect(() => {
        if (audioRef.current) {
            audioRef.current.playbackRate = playbackRate;
        }
    }, [playbackRate]);

    const togglePlay = async () => {
        if (!audioRef.current || !audioUrl) return;

        if (isPlaying) {
            audioRef.current.pause();
        } else {
            // Mark as played if not already from me and haven't marked it yet
            if (!isFromMe && !hasPlayedAndMarked) {
                try {
                    // Fire and forget, don't block playback
                    MarkMessageAsPlayed(conversationID, messageID).catch(console.error);
                    setHasPlayedAndMarked(true);
                } catch (err) {
                    console.error("Error marking voice message as played:", err);
                }
            }
            try {
                await audioRef.current.play();
            } catch (err) {
                console.error("Playback failed:", err);
            }
        }
        setIsPlaying(!isPlaying);
    };

    const handleTimeUpdate = () => {
        if (audioRef.current) {
            setProgress(audioRef.current.currentTime);
            // Update duration if it wasn't provided or slightly off
            if (!attachment.duration && audioRef.current.duration) {
                setDuration(audioRef.current.duration);
            }
        }
    };

    const handleEnded = () => {
        setIsPlaying(false);
        setProgress(0);
        if (audioRef.current) {
            audioRef.current.currentTime = 0;
        }
    };

    const handleSliderChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const newTime = parseFloat(e.target.value);
        setProgress(newTime);
        if (audioRef.current) {
            audioRef.current.currentTime = newTime;
        }
    };

    const toggleSpeed = () => {
        const speeds = [1, 1.5, 2];
        const nextIndex = (speeds.indexOf(playbackRate) + 1) % speeds.length;
        setPlaybackRate(speeds[nextIndex]);
    };

    const formatTime = (time: number) => {
        const invalid = isNaN(time) || !isFinite(time);
        if (invalid) return "0:00";

        const minutes = Math.floor(time / 60);
        const seconds = Math.floor(time % 60);
        return `${minutes}:${seconds.toString().padStart(2, "0")}`;
    };

    // Width classes based on layout
    const widthClass = layout === "irc" ? "max-w-[33%] min-w-[300px] w-full" : "min-w-[300px]";

    // Waveform rendering
    const renderWaveform = () => {
        if (waveform.length === 0) {
            // Fallback to slider
            return (
                <input
                    type="range"
                    min={0}
                    max={duration || 100}
                    value={progress}
                    onChange={handleSliderChange}
                    className={`w-full h-1.5 rounded-full appearance-none cursor-pointer [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-3 [&::-webkit-slider-thumb]:h-3 [&::-webkit-slider-thumb]:rounded-full ${isFromMe
                        ? "bg-white/30 [&::-webkit-slider-thumb]:bg-white"
                        : "bg-primary/20 [&::-webkit-slider-thumb]:bg-primary"
                        }`}
                    style={{
                        backgroundImage: `linear-gradient(to right, ${isFromMe ? "white" : "currentColor"} ${(progress / (duration || 1)) * 100}%, transparent ${(progress / (duration || 1)) * 100}%)`
                    }}
                />
            );
        }

        // Render waveform
        return (
            <div className="flex items-center gap-[2px] h-8 w-full cursor-pointer" onClick={(e) => {
                const rect = e.currentTarget.getBoundingClientRect();
                const x = e.clientX - rect.left;
                const percentage = x / rect.width;
                const newTime = percentage * (duration || 1);
                setProgress(newTime);
                if (audioRef.current) {
                    audioRef.current.currentTime = newTime;
                }
            }}>
                {waveform.map((amp, i) => {
                    // Determine if this bar is "played"
                    const barProgress = i / waveform.length;
                    const currentProgress = progress / (duration || 0.1); // avoid div by zero
                    const isPlayed = barProgress <= currentProgress;

                    // Min height 20%, max 100%
                    const height = Math.max(20, amp * 100) + "%";

                    return (
                        <div
                            key={i}
                            className={`flex-1 rounded-full transition-colors ${isPlayed
                                ? (isFromMe ? "bg-white" : "bg-primary")
                                : (isFromMe ? "bg-white/40" : "bg-primary/30")
                                }`}
                            style={{ height }}
                        />
                    )
                })}
            </div>
        );
    }

    return (
        <div className={`flex items-center gap-3 px-3 ${layout === "bubble" ? "py-1" : "py-2"} rounded-xl ${widthClass} ${isFromMe ? "bg-blue-600 text-white" : "bg-muted/50 text-foreground border border-border"
            }`}>
            {audioUrl && (
                <audio
                    ref={audioRef}
                    src={audioUrl}
                    onTimeUpdate={handleTimeUpdate}
                    onEnded={handleEnded}
                    onLoadedMetadata={(e) => {
                        if (!attachment.duration) {
                            setDuration(e.currentTarget.duration);
                        }
                    }}
                    className="hidden"
                />
            )}

            <button
                onClick={togglePlay}
                disabled={!audioUrl}
                className={`flex items-center justify-center h-10 w-10 rounded-full shrink-0 transition-colors ${isFromMe
                    ? "bg-white/20 hover:bg-white/30 text-white"
                    : "bg-primary/10 hover:bg-primary/20 text-primary"
                    }`}
            >
                {isPlaying ? (
                    <Pause className="h-5 w-5 fill-current" />
                ) : (
                    <Play className="h-5 w-5 fill-current ml-0.5" />
                )}
            </button>

            <div className="flex-1 flex flex-col gap-1 min-w-0">
                {renderWaveform()}
                <div className={`flex justify-between text-xs ${isFromMe ? "text-white/70" : "text-muted-foreground"}`}>
                    <span>{formatTime(progress)}</span>
                    <span>{formatTime(duration)}</span>
                </div>
            </div>

            <button
                onClick={toggleSpeed}
                className={`text-xs font-medium px-2 py-1 rounded-md transition-colors ${isFromMe
                    ? "bg-white/20 hover:bg-white/30 text-white"
                    : "bg-primary/10 hover:bg-primary/20 text-primary"
                    }`}
            >
                {playbackRate}x
            </button>
        </div>
    );
}

import { GetAttachmentData, MarkMessageAsPlayed } from "../../wailsjs/go/main/App";
import { Loader2, Pause, Play } from "lucide-react";
import { useEffect, useRef, useState } from "react";

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
    const [loadRequested, setLoadRequested] = useState(false);
    const [playWhenReady, setPlayWhenReady] = useState(false);

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
        let objectUrl: string | null = null;
        let audioContext: AudioContext | null = null;
        setAudioUrl(null);
        setWaveform([]);
        const loadAudio = async () => {
            if (!attachment.url || !loadRequested) return;
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
                        if (channelData.length > 0 && active) {
                            setWaveform(calculateWaveform(channelData[0]));
                        }

                        // WhatsApp voice notes are usually mono (channelData[0])
                        // If stereo, we'd need to interleave, but let's assume mono/take first channel for now
                        const wavBlob = encodeWAV(channelData[0], sampleRate);
                        const url = URL.createObjectURL(wavBlob);
                        objectUrl = url;

                        if (active) {
                            setAudioUrl(url);
                        } else {
                            URL.revokeObjectURL(url);
                        }
                        return;
                    } catch (decodeErr) {
                        console.warn("[VoiceMessage] Failed to decode OGG, falling back to original source:", decodeErr);
                    }
                }

                // Fallback for supported formats (MP3/M4A) - decode with Web Audio API to generate waveform
                console.log("[VoiceMessage] Attempting Web Audio API decoding for waveform...");
                try {
                    audioContext = new (window.AudioContext || (window as any).webkitAudioContext)();
                    
                    // Convert data URL to ArrayBuffer
                    const response = await fetch(data);
                    const arrayBuffer = await response.arrayBuffer();
                    
                    // Decode audio data
                    const audioBuffer = await audioContext.decodeAudioData(arrayBuffer);

                    // Generate waveform from decoded audio
                    const samples = audioBuffer.getChannelData(0); // Use first channel
                    const calculatedWaveform = calculateWaveform(samples);
                    if (active) setWaveform(calculatedWaveform);
                    console.log("[VoiceMessage] Generated waveform from Web Audio API", { 
                        sampleRate: audioBuffer.sampleRate, 
                        duration: audioBuffer.duration,
                        waveformBars: calculatedWaveform.length
                    });
                    
                    // Update duration if available
                    if (audioBuffer.duration > 0 && !attachment.duration) {
                        if (active) setDuration(audioBuffer.duration);
                    }
                    
                    if (active) setAudioUrl(data);
                } catch (webAudioErr) {
                    console.warn("[VoiceMessage] Web Audio API decoding failed, using native playback:", webAudioErr);
                    if (active) setAudioUrl(data);
                    // Waveform will remain empty, showing fallback slider
                } finally {
                    await audioContext?.close();
                }
            } catch (err) {
                console.error("Failed to load voice message:", err);
            }
        };
        loadAudio();
        return () => {
            active = false;
            if (objectUrl) URL.revokeObjectURL(objectUrl);
            void audioContext?.close();
        };
    }, [attachment.url, attachment.fileName, loadRequested]);

    // Handle playback rate
    useEffect(() => {
        if (audioRef.current) {
            audioRef.current.playbackRate = playbackRate;
        }
    }, [playbackRate]);

    // Preserve the expected one-click behaviour while still deferring the
    // costly fetch/decode until the user elects to play a voice note.
    useEffect(() => {
        if (!playWhenReady || !audioUrl || !audioRef.current) return;
        setPlayWhenReady(false);
        audioRef.current.play()
            .then(() => {
                setIsPlaying(true);
                if (!isFromMe && !hasPlayedAndMarked) {
                    MarkMessageAsPlayed(conversationID, messageID).catch(console.error);
                    setHasPlayedAndMarked(true);
                }
            })
            .catch((err) => console.error("Playback failed:", err));
    }, [audioUrl, conversationID, hasPlayedAndMarked, isFromMe, messageID, playWhenReady]);

    const togglePlay = async () => {
        if (!loadRequested) {
            setLoadRequested(true);
            setPlayWhenReady(true);
            return;
        }
        if (!audioUrl) {
            setPlayWhenReady(true);
            return;
        }
        if (!audioRef.current) return;

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
            <div className="flex items-center gap-[2px] h-full w-full cursor-pointer" onClick={(e) => {
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
                disabled={loadRequested && !audioUrl}
                className={`flex items-center justify-center h-10 w-10 rounded-full shrink-0 transition-colors ${isFromMe
                    ? "bg-white/20 hover:bg-white/30 text-white"
                    : "bg-primary/10 hover:bg-primary/20 text-primary"
                    }`}
            >
                {!audioUrl && loadRequested && <Loader2 className="h-5 w-5 animate-spin opacity-60" />}
                {!audioUrl && !loadRequested && <Play className="h-5 w-5 fill-current ml-0.5" />}
                {audioUrl && isPlaying && <Pause className="h-5 w-5 fill-current" />}
                {audioUrl && !isPlaying && <Play className="h-5 w-5 fill-current ml-0.5" />}
            </button>

            <div className="flex-1 flex flex-col gap-1 min-w-0">
                {/* Fixed h-8 container so slider↔waveform swap doesn't change item height */}
                <div className="h-8 flex items-center w-full">
                  {!audioUrl ? (
                      <div className="flex items-center gap-[2px] h-full w-full">
                          {Array.from({ length: 40 }, (_, i) => (
                              <div
                                  key={i}
                                  className={`flex-1 rounded-full animate-pulse ${isFromMe ? "bg-white/30" : "bg-primary/20"}`}
                                  style={{ height: `${20 + Math.sin(i * 0.8) * 15 + 15}%` }}
                              />
                          ))}
                      </div>
                  ) : renderWaveform()}
                </div>
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

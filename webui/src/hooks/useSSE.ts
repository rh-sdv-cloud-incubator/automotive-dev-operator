import { useEffect, useRef, useState, useCallback } from 'react';

export interface SSEMessage {
  event: string;
  data: string;
  id?: string;
}

export interface SSEHookOptions {
  onMessage?: (message: SSEMessage) => void;
  onError?: (error: Event) => void;
  onOpen?: () => void;
  onClose?: () => void;
  autoReconnect?: boolean;
  reconnectInterval?: number;
  maxReconnectAttempts?: number;
}

export interface SSEHookReturn {
  isConnected: boolean;
  isConnecting: boolean;
  error: string | null;
  connect: () => void;
  disconnect: () => void;
  lastMessage: SSEMessage | null;
}

export const useSSE = (url: string | null, options: SSEHookOptions = {}): SSEHookReturn => {
  const {
    onMessage,
    onError,
    onOpen,
    onClose,
    autoReconnect = true,
    reconnectInterval = 3000,
    maxReconnectAttempts = 5,
  } = options;

  const [isConnected, setIsConnected] = useState(false);
  const [isConnecting, setIsConnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lastMessage, setLastMessage] = useState<SSEMessage | null>(null);

  const abortControllerRef = useRef<AbortController | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const shouldReconnectRef = useRef(true);

  const cleanup = useCallback(() => {
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }
  }, []);

  const connect = useCallback(async () => {
    if (!url || abortControllerRef.current) {
      return;
    }

    cleanup();
    setIsConnecting(true);
    setError(null);
    shouldReconnectRef.current = true;

    try {
      const abortController = new AbortController();
      abortControllerRef.current = abortController;

      const finalUrl = url.startsWith('/v1') ? url : (url.startsWith('http') ? url : `/${url}`);

      const response = await fetch(finalUrl, {
        method: 'GET',
        headers: {
          'Accept': 'text/event-stream',
          'Cache-Control': 'no-cache',
        },
        credentials: 'include',
        signal: abortController.signal,
      });

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      if (!response.body) {
        throw new Error('Response body is null');
      }

      setIsConnected(true);
      setIsConnecting(false);
      setError(null);
      reconnectAttemptsRef.current = 0;
      onOpen?.();

          const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      try {
        while (true) {
          const { done, value } = await reader.read();

          if (done) {
            console.log('SSE stream ended (done=true)');
            break;
          }

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop() || '';

          let currentEvent = 'message';
          let currentId: string | undefined = undefined;
          let currentData = '';

          const sendCurrentMessage = () => {
            if (currentData) {
              const message: SSEMessage = {
                event: currentEvent,
                data: currentData,
                id: currentId,
              };
              setLastMessage(message);
              onMessage?.(message);
              // Reset for next message
              currentEvent = 'message';
              currentId = undefined;
              currentData = '';
            }
          };

          for (const line of lines) {
            if (line.trim() === '') {
              // Empty line indicates end of event, send the accumulated message
              sendCurrentMessage();
              continue;
            }

            if (line.startsWith('event:')) {
              currentEvent = line.substring(6).trim();
            } else if (line.startsWith('id:')) {
              currentId = line.substring(3).trim();
            } else if (line.startsWith('data:')) {
              currentData = line.substring(5).trim();
            }
          }

          // Send any remaining message at the end of the chunk
          sendCurrentMessage();
        }
      } finally {
        reader.releaseLock();
      }

    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') {
        // Connection was aborted, don't treat as error
        console.log('SSE connection aborted');
        return;
      }

      console.error('SSE connection error:', err);
      setIsConnected(false);
      setIsConnecting(false);
      setError(err instanceof Error ? err.message : 'Unknown error');
      onError?.(err as Event);

      if (shouldReconnectRef.current && autoReconnect && reconnectAttemptsRef.current < maxReconnectAttempts) {
        reconnectAttemptsRef.current++;
        reconnectTimeoutRef.current = setTimeout(() => {
          if (shouldReconnectRef.current) {
            connect();
          }
        }, reconnectInterval);
      } else if (reconnectAttemptsRef.current >= maxReconnectAttempts) {
        setError(`Failed to reconnect after ${maxReconnectAttempts} attempts`);
      }
    }
  }, [url, onMessage, onError, onOpen, autoReconnect, reconnectInterval, maxReconnectAttempts, cleanup]);

  const disconnect = useCallback(() => {
    shouldReconnectRef.current = false;
    cleanup();
    setIsConnected(false);
    setIsConnecting(false);
    onClose?.();
  }, [cleanup, onClose]);

  // Auto-connect when URL changes
  useEffect(() => {
    if (url) {
      connect();
    } else {
      disconnect();
    }

    return () => {
      disconnect();
    };
  }, [url, connect, disconnect]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      disconnect();
    };
  }, [disconnect]);

  return {
    isConnected,
    isConnecting,
    error,
    connect,
    disconnect,
    lastMessage,
  };
};
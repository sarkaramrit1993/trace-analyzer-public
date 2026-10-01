/**
 * Comprehensive Robustness Tests for Span Validation and Sanitization Utilities
 *
 * Tests all functions with malformed, invalid, and edge case inputs to ensure
 * defensive handling and prevent UI crashes from bad data.
 */

import { describe, it, expect } from 'vitest';
import type { Span, SpanUI } from '../types';
import {
  isValidDuration,
  isValidTimestamp,
  isValidSpanUI,
  isValidSpan,
  sanitizeDuration,
  sanitizeTimestamp,
  sanitizeStatus,
  sanitizeAttributes,
  sanitizeString,
  sanitizeSpanUI,
  sanitizeSpan,
  sanitizeSpansUI,
  sanitizeSpans,
  detectCircularReferences,
  detectCircularReferencesSpan,
  DEFAULTS,
  MAX_SPAN_DEPTH,
} from './spanValidation';

describe('spanValidation', () => {
  describe('isValidDuration', () => {
    it('should reject NaN', () => {
      expect(isValidDuration(NaN)).toBe(false);
    });

    it('should reject Infinity', () => {
      expect(isValidDuration(Infinity)).toBe(false);
    });

    it('should reject -Infinity', () => {
      expect(isValidDuration(-Infinity)).toBe(false);
    });

    it('should reject null', () => {
      expect(isValidDuration(null)).toBe(false);
    });

    it('should reject undefined', () => {
      expect(isValidDuration(undefined)).toBe(false);
    });

    it('should reject string', () => {
      expect(isValidDuration('123')).toBe(false);
      expect(isValidDuration('invalid')).toBe(false);
    });

    it('should reject object', () => {
      expect(isValidDuration({})).toBe(false);
      expect(isValidDuration({ value: 123 })).toBe(false);
    });

    it('should reject negative numbers', () => {
      expect(isValidDuration(-1)).toBe(false);
      expect(isValidDuration(-100)).toBe(false);
      expect(isValidDuration(-0.5)).toBe(false);
    });

    it('should accept 0 as valid edge case', () => {
      expect(isValidDuration(0)).toBe(true);
    });

    it('should accept positive numbers', () => {
      expect(isValidDuration(1)).toBe(true);
      expect(isValidDuration(100.5)).toBe(true);
      expect(isValidDuration(1000000)).toBe(true);
    });
  });

  describe('isValidTimestamp', () => {
    it('should reject NaN', () => {
      expect(isValidTimestamp(NaN)).toBe(false);
    });

    it('should reject Infinity', () => {
      expect(isValidTimestamp(Infinity)).toBe(false);
    });

    it('should reject -Infinity', () => {
      expect(isValidTimestamp(-Infinity)).toBe(false);
    });

    it('should reject null', () => {
      expect(isValidTimestamp(null)).toBe(false);
    });

    it('should reject undefined', () => {
      expect(isValidTimestamp(undefined)).toBe(false);
    });

    it('should reject string', () => {
      expect(isValidTimestamp('123')).toBe(false);
      expect(isValidTimestamp('invalid')).toBe(false);
    });

    it('should reject object', () => {
      expect(isValidTimestamp({})).toBe(false);
      expect(isValidTimestamp({ value: 123 })).toBe(false);
    });

    it('should accept negative numbers (valid for timestamps)', () => {
      expect(isValidTimestamp(-1)).toBe(true);
      expect(isValidTimestamp(-100)).toBe(true);
      expect(isValidTimestamp(-1000000)).toBe(true);
    });

    it('should accept 0 as valid edge case', () => {
      expect(isValidTimestamp(0)).toBe(true);
    });

    it('should accept positive numbers', () => {
      expect(isValidTimestamp(1)).toBe(true);
      expect(isValidTimestamp(100.5)).toBe(true);
      expect(isValidTimestamp(1000000)).toBe(true);
    });
  });

  describe('isValidSpanUI', () => {
    it('should reject null', () => {
      expect(isValidSpanUI(null)).toBe(false);
    });

    it('should reject undefined', () => {
      expect(isValidSpanUI(undefined)).toBe(false);
    });

    it('should reject empty object', () => {
      expect(isValidSpanUI({})).toBe(false);
    });

    it('should reject object with empty span_id', () => {
      expect(isValidSpanUI({ span_id: '' })).toBe(false);
    });

    it('should reject object with non-string span_id', () => {
      expect(isValidSpanUI({ span_id: 123 })).toBe(false);
      expect(isValidSpanUI({ span_id: null })).toBe(false);
      expect(isValidSpanUI({ span_id: undefined })).toBe(false);
      expect(isValidSpanUI({ span_id: {} })).toBe(false);
    });

    it('should accept object with only span_id (valid partial)', () => {
      expect(isValidSpanUI({ span_id: 'span-123' })).toBe(true);
    });

    it('should accept complete SpanUI object', () => {
      const validSpan: SpanUI = {
        span_id: 'span-123',
        parent_span_id: 'parent-123',
        service_name: 'test-service',
        operation_name: 'test-operation',
        duration_us: 1000,
        start_time: 1234567890,
        status: 'OK',
        attributes: {},
      };
      expect(isValidSpanUI(validSpan)).toBe(true);
    });

    it('should reject primitives', () => {
      expect(isValidSpanUI(123)).toBe(false);
      expect(isValidSpanUI('string')).toBe(false);
      expect(isValidSpanUI(true)).toBe(false);
    });

    it('should reject arrays', () => {
      expect(isValidSpanUI([])).toBe(false);
      expect(isValidSpanUI([{ span_id: 'span-123' }])).toBe(false);
    });
  });

  describe('isValidSpan', () => {
    it('should reject null', () => {
      expect(isValidSpan(null)).toBe(false);
    });

    it('should reject undefined', () => {
      expect(isValidSpan(undefined)).toBe(false);
    });

    it('should reject empty object', () => {
      expect(isValidSpan({})).toBe(false);
    });

    it('should reject object with empty SpanID', () => {
      expect(isValidSpan({ SpanID: '' })).toBe(false);
    });

    it('should reject object with non-string SpanID', () => {
      expect(isValidSpan({ SpanID: 123 })).toBe(false);
      expect(isValidSpan({ SpanID: null })).toBe(false);
      expect(isValidSpan({ SpanID: undefined })).toBe(false);
      expect(isValidSpan({ SpanID: {} })).toBe(false);
    });

    it('should accept object with only SpanID (valid partial)', () => {
      expect(isValidSpan({ SpanID: 'span-123' })).toBe(true);
    });

    it('should accept complete Span object', () => {
      const validSpan: Span = {
        SpanID: 'span-123',
        ParentID: 'parent-123',
        TraceID: 'trace-123',
        ServiceName: 'test-service',
        OperationName: 'test-operation',
        Duration: 1000,
        StartTime: 1234567890,
        Tags: {},
      };
      expect(isValidSpan(validSpan)).toBe(true);
    });

    it('should reject primitives', () => {
      expect(isValidSpan(123)).toBe(false);
      expect(isValidSpan('string')).toBe(false);
      expect(isValidSpan(true)).toBe(false);
    });

    it('should reject arrays', () => {
      expect(isValidSpan([])).toBe(false);
      expect(isValidSpan([{ SpanID: 'span-123' }])).toBe(false);
    });
  });

  describe('sanitizeDuration', () => {
    it('should return 0 for NaN', () => {
      expect(sanitizeDuration(NaN)).toBe(DEFAULTS.DURATION);
    });

    it('should return 0 for null', () => {
      expect(sanitizeDuration(null)).toBe(DEFAULTS.DURATION);
    });

    it('should return 0 for undefined', () => {
      expect(sanitizeDuration(undefined)).toBe(DEFAULTS.DURATION);
    });

    it('should return 0 for negative values', () => {
      expect(sanitizeDuration(-1)).toBe(DEFAULTS.DURATION);
      expect(sanitizeDuration(-100)).toBe(DEFAULTS.DURATION);
    });

    it('should convert string numbers to numbers', () => {
      expect(sanitizeDuration('123')).toBe(123);
      expect(sanitizeDuration('0')).toBe(0);
      expect(sanitizeDuration('100.5')).toBe(100.5);
    });

    it('should return 0 for invalid string numbers', () => {
      expect(sanitizeDuration('invalid')).toBe(DEFAULTS.DURATION);
      expect(sanitizeDuration('abc')).toBe(DEFAULTS.DURATION);
    });

    it('should return 0 for negative string numbers', () => {
      expect(sanitizeDuration('-123')).toBe(DEFAULTS.DURATION);
    });

    it('should return valid numbers unchanged', () => {
      expect(sanitizeDuration(0)).toBe(0);
      expect(sanitizeDuration(100)).toBe(100);
      expect(sanitizeDuration(1234.56)).toBe(1234.56);
    });

    it('should return 0 for Infinity', () => {
      expect(sanitizeDuration(Infinity)).toBe(DEFAULTS.DURATION);
      expect(sanitizeDuration(-Infinity)).toBe(DEFAULTS.DURATION);
    });

    it('should return 0 for objects', () => {
      expect(sanitizeDuration({})).toBe(DEFAULTS.DURATION);
      expect(sanitizeDuration({ value: 123 })).toBe(DEFAULTS.DURATION);
    });
  });

  describe('sanitizeTimestamp', () => {
    it('should return 0 for NaN', () => {
      expect(sanitizeTimestamp(NaN)).toBe(0);
    });

    it('should return 0 for null', () => {
      expect(sanitizeTimestamp(null)).toBe(0);
    });

    it('should return 0 for undefined', () => {
      expect(sanitizeTimestamp(undefined)).toBe(0);
    });

    it('should accept negative values (valid for timestamps)', () => {
      expect(sanitizeTimestamp(-1)).toBe(-1);
      expect(sanitizeTimestamp(-100)).toBe(-100);
      expect(sanitizeTimestamp(-1234567890)).toBe(-1234567890);
    });

    it('should convert string numbers to numbers', () => {
      expect(sanitizeTimestamp('123')).toBe(123);
      expect(sanitizeTimestamp('0')).toBe(0);
      expect(sanitizeTimestamp('-456')).toBe(-456);
    });

    it('should return 0 for invalid string numbers', () => {
      expect(sanitizeTimestamp('invalid')).toBe(0);
      expect(sanitizeTimestamp('abc')).toBe(0);
    });

    it('should return valid numbers unchanged', () => {
      expect(sanitizeTimestamp(0)).toBe(0);
      expect(sanitizeTimestamp(100)).toBe(100);
      expect(sanitizeTimestamp(-100)).toBe(-100);
      expect(sanitizeTimestamp(1234.56)).toBe(1234.56);
    });

    it('should return 0 for Infinity', () => {
      expect(sanitizeTimestamp(Infinity)).toBe(0);
      expect(sanitizeTimestamp(-Infinity)).toBe(0);
    });

    it('should return 0 for objects', () => {
      expect(sanitizeTimestamp({})).toBe(0);
      expect(sanitizeTimestamp({ value: 123 })).toBe(0);
    });
  });

  describe('sanitizeStatus', () => {
    it('should return ERROR for ERROR string', () => {
      expect(sanitizeStatus('ERROR')).toBe('ERROR');
    });

    it('should return OK for OK string', () => {
      expect(sanitizeStatus('OK')).toBe('OK');
    });

    it('should return OK for null', () => {
      expect(sanitizeStatus(null)).toBe('OK');
    });

    it('should return OK for undefined', () => {
      expect(sanitizeStatus(undefined)).toBe('OK');
    });

    it('should return OK for random strings', () => {
      expect(sanitizeStatus('INVALID')).toBe('OK');
      expect(sanitizeStatus('error')).toBe('OK'); // case sensitive
      expect(sanitizeStatus('SUCCESS')).toBe('OK');
      expect(sanitizeStatus('random')).toBe('OK');
    });

    it('should return OK for numbers', () => {
      expect(sanitizeStatus(123)).toBe('OK');
      expect(sanitizeStatus(0)).toBe('OK');
    });

    it('should return OK for objects', () => {
      expect(sanitizeStatus({})).toBe('OK');
      expect(sanitizeStatus({ status: 'ERROR' })).toBe('OK');
    });

    it('should return OK for boolean', () => {
      expect(sanitizeStatus(true)).toBe('OK');
      expect(sanitizeStatus(false)).toBe('OK');
    });
  });

  describe('sanitizeAttributes', () => {
    it('should return empty object for null', () => {
      expect(sanitizeAttributes(null)).toEqual({});
    });

    it('should return empty object for undefined', () => {
      expect(sanitizeAttributes(undefined)).toEqual({});
    });

    it('should return empty object for array', () => {
      expect(sanitizeAttributes([])).toEqual({});
      expect(sanitizeAttributes([1, 2, 3])).toEqual({});
      expect(sanitizeAttributes(['a', 'b'])).toEqual({});
    });

    it('should return valid object unchanged', () => {
      const validObj = { key1: 'value1', key2: 'value2' };
      expect(sanitizeAttributes(validObj)).toBe(validObj);
    });

    it('should return empty object unchanged', () => {
      const emptyObj = {};
      expect(sanitizeAttributes(emptyObj)).toBe(emptyObj);
    });

    it('should return empty object for primitives', () => {
      expect(sanitizeAttributes(123)).toEqual({});
      expect(sanitizeAttributes('string')).toEqual({});
      expect(sanitizeAttributes(true)).toEqual({});
    });

    it('should accept object with various value types', () => {
      const obj = { str: 'value', num: 123, bool: true };
      const result = sanitizeAttributes(obj);
      expect(result).toBe(obj);
    });
  });

  describe('sanitizeString', () => {
    it('should return fallback for null', () => {
      expect(sanitizeString(null, 'default')).toBe('default');
    });

    it('should return fallback for undefined', () => {
      expect(sanitizeString(undefined, 'default')).toBe('default');
    });

    it('should return fallback for empty string', () => {
      expect(sanitizeString('', 'default')).toBe('default');
    });

    it('should return fallback for non-string', () => {
      expect(sanitizeString(123, 'default')).toBe('default');
      expect(sanitizeString({}, 'default')).toBe('default');
      expect(sanitizeString([], 'default')).toBe('default');
    });

    it('should return valid string unchanged', () => {
      expect(sanitizeString('valid', 'default')).toBe('valid');
      expect(sanitizeString('test string', 'default')).toBe('test string');
    });
  });

  describe('sanitizeSpanUI', () => {
    it('should generate default span with unique ID for null', () => {
      const result = sanitizeSpanUI(null);
      expect(result.span_id).toMatch(/^unknown-/);
      expect(result.parent_span_id).toBe('');
      expect(result).not.toHaveProperty('trace_id');
      expect(result.service_name).toBe(DEFAULTS.SERVICE);
      expect(result.operation_name).toBe(DEFAULTS.OPERATION);
      expect(result.duration_us).toBe(DEFAULTS.DURATION);
      expect(result.start_time).toBe(0);
      expect(result.status).toBe(DEFAULTS.STATUS);
      expect(result.attributes).toEqual({});
    });

    it('should generate default span with unique ID for undefined', () => {
      const result = sanitizeSpanUI(undefined);
      expect(result.span_id).toMatch(/^unknown-/);
      expect(result.parent_span_id).toBe('');
      expect(result).not.toHaveProperty('trace_id');
    });

    it('should fill in defaults for empty object', () => {
      const result = sanitizeSpanUI({});
      expect(result.span_id).toMatch(/^unknown-/);
      expect(result.parent_span_id).toBe('');
      expect(result).not.toHaveProperty('trace_id');
      expect(result.service_name).toBe(DEFAULTS.SERVICE);
      expect(result.operation_name).toBe(DEFAULTS.OPERATION);
      expect(result.duration_us).toBe(DEFAULTS.DURATION);
      expect(result.start_time).toBe(0);
      expect(result.status).toBe(DEFAULTS.STATUS);
      expect(result.attributes).toEqual({});
    });

    it('should fill in missing fields for partial span', () => {
      const partial = { span_id: 'span-123', service_name: 'test-service' };
      const result = sanitizeSpanUI(partial);
      expect(result.span_id).toBe('span-123');
      expect(result.service_name).toBe('test-service');
      expect(result.parent_span_id).toBe('');
      expect(result.operation_name).toBe(DEFAULTS.OPERATION);
      expect(result.duration_us).toBe(DEFAULTS.DURATION);
      expect(result.start_time).toBe(0);
      expect(result.status).toBe(DEFAULTS.STATUS);
      expect(result.attributes).toEqual({});
    });

    it('should return sanitized version of complete span', () => {
      const complete: SpanUI = {
        span_id: 'span-123',
        parent_span_id: 'parent-123',
        service_name: 'test-service',
        operation_name: 'test-operation',
        duration_us: 1000,
        start_time: 1234567890,
        status: 'ERROR',
        attributes: { key: 'value' },
      };
      const result = sanitizeSpanUI(complete);
      expect(result).toEqual(complete);
    });

    it('should sanitize malformed fields in otherwise valid span', () => {
      const malformed = {
        span_id: 'span-123',
        parent_span_id: 123, // should be string
        service_name: '', // should use default
        duration_us: -100, // should be 0
        start_time: 'invalid', // should be 0
        status: 'INVALID', // should be OK
        attributes: [1, 2, 3], // should be {}
      } as unknown as Partial<SpanUI>;

      const result = sanitizeSpanUI(malformed);
      expect(result.span_id).toBe('span-123');
      expect(result.parent_span_id).toBe('');
      expect(result.service_name).toBe(DEFAULTS.SERVICE);
      expect(result.duration_us).toBe(DEFAULTS.DURATION);
      expect(result.start_time).toBe(0);
      expect(result.status).toBe('OK');
      expect(result.attributes).toEqual({});
    });

    it('should generate unique IDs for multiple null inputs', () => {
      const result1 = sanitizeSpanUI(null);
      const result2 = sanitizeSpanUI(null);
      expect(result1.span_id).not.toBe(result2.span_id);
    });
  });

  describe('sanitizeSpan', () => {
    it('should generate default span with unique ID for null', () => {
      const result = sanitizeSpan(null);
      expect(result.SpanID).toMatch(/^unknown-/);
      expect(result.ParentID).toBe('');
      expect(result.TraceID).toBe('unknown');
      expect(result.ServiceName).toBe(DEFAULTS.SERVICE);
      expect(result.OperationName).toBe(DEFAULTS.OPERATION);
      expect(result.Duration).toBe(DEFAULTS.DURATION);
      expect(result.StartTime).toBe(0);
      expect(result.Tags).toEqual({});
    });

    it('should generate default span with unique ID for undefined', () => {
      const result = sanitizeSpan(undefined);
      expect(result.SpanID).toMatch(/^unknown-/);
      expect(result.ParentID).toBe('');
      expect(result.TraceID).toBe('unknown');
    });

    it('should fill in defaults for empty object', () => {
      const result = sanitizeSpan({});
      expect(result.SpanID).toMatch(/^unknown-/);
      expect(result.ParentID).toBe('');
      expect(result.TraceID).toBe('unknown');
      expect(result.ServiceName).toBe(DEFAULTS.SERVICE);
      expect(result.OperationName).toBe(DEFAULTS.OPERATION);
      expect(result.Duration).toBe(DEFAULTS.DURATION);
      expect(result.StartTime).toBe(0);
      expect(result.Tags).toEqual({});
    });

    it('should fill in missing fields for partial span', () => {
      const partial = { SpanID: 'span-123', ServiceName: 'test-service' };
      const result = sanitizeSpan(partial);
      expect(result.SpanID).toBe('span-123');
      expect(result.ServiceName).toBe('test-service');
      expect(result.ParentID).toBe('');
      expect(result.OperationName).toBe(DEFAULTS.OPERATION);
      expect(result.Duration).toBe(DEFAULTS.DURATION);
      expect(result.StartTime).toBe(0);
      expect(result.Tags).toEqual({});
    });

    it('should return sanitized version of complete span', () => {
      const complete: Span = {
        SpanID: 'span-123',
        ParentID: 'parent-123',
        TraceID: 'trace-123',
        ServiceName: 'test-service',
        OperationName: 'test-operation',
        Duration: 1000,
        StartTime: 1234567890,
        Tags: { key: 'value' },
      };
      const result = sanitizeSpan(complete);
      expect(result).toEqual(complete);
    });

    it('should sanitize malformed fields in otherwise valid span', () => {
      const malformed = {
        SpanID: 'span-123',
        ParentID: 123, // should be string
        ServiceName: '', // should use default
        Duration: -100, // should be 0
        StartTime: 'invalid', // should be 0
        Tags: [1, 2, 3], // should be {}
      } as unknown as Partial<Span>;

      const result = sanitizeSpan(malformed);
      expect(result.SpanID).toBe('span-123');
      expect(result.ParentID).toBe('');
      expect(result.ServiceName).toBe(DEFAULTS.SERVICE);
      expect(result.Duration).toBe(DEFAULTS.DURATION);
      expect(result.StartTime).toBe(0);
      expect(result.Tags).toEqual({});
    });

    it('should generate unique IDs for multiple null inputs', () => {
      const result1 = sanitizeSpan(null);
      const result2 = sanitizeSpan(null);
      expect(result1.SpanID).not.toBe(result2.SpanID);
    });
  });

  describe('sanitizeSpansUI', () => {
    it('should return empty array when input is not an array', () => {
      expect(sanitizeSpansUI(null)).toEqual([]);
      expect(sanitizeSpansUI(undefined)).toEqual([]);
      expect(sanitizeSpansUI('string')).toEqual([]);
      expect(sanitizeSpansUI(123)).toEqual([]);
      expect(sanitizeSpansUI({})).toEqual([]);
    });

    it('should filter out null elements', () => {
      const input = [
        { span_id: 'span-1', service_name: 'service-1' },
        null,
        { span_id: 'span-2', service_name: 'service-2' },
      ];
      const result = sanitizeSpansUI(input);
      expect(result).toHaveLength(2);
      expect(result[0].span_id).toBe('span-1');
      expect(result[1].span_id).toBe('span-2');
    });

    it('should filter out undefined elements', () => {
      const input = [
        { span_id: 'span-1', service_name: 'service-1' },
        undefined,
        { span_id: 'span-2', service_name: 'service-2' },
      ];
      const result = sanitizeSpansUI(input);
      expect(result).toHaveLength(2);
    });

    it('should filter out invalid spans (primitives)', () => {
      const input = [
        { span_id: 'span-1', service_name: 'service-1' },
        123,
        'string',
        true,
        { span_id: 'span-2', service_name: 'service-2' },
      ];
      const result = sanitizeSpansUI(input);
      expect(result).toHaveLength(2);
    });

    it('should filter out spans without valid span_id', () => {
      const input = [
        { span_id: 'span-1', service_name: 'service-1' },
        {}, // no span_id
        { span_id: '', service_name: 'service-2' }, // empty span_id
        { span_id: 'span-3', service_name: 'service-3' },
      ];
      const result = sanitizeSpansUI(input);
      expect(result).toHaveLength(2);
      expect(result[0].span_id).toBe('span-1');
      expect(result[1].span_id).toBe('span-3');
    });

    it('should sanitize and return valid spans', () => {
      const input = [
        {
          span_id: 'span-1',
          service_name: 'service-1',
          duration_us: 1000,
        },
        {
          span_id: 'span-2',
          service_name: 'service-2',
          duration_us: -100, // should be sanitized to 0
        },
      ];
      const result = sanitizeSpansUI(input);
      expect(result).toHaveLength(2);
      expect(result[0].duration_us).toBe(1000);
      expect(result[1].duration_us).toBe(0);
    });

    it('should return empty array for array with only invalid elements', () => {
      const input = [null, undefined, 123, 'string', {}];
      const result = sanitizeSpansUI(input);
      expect(result).toEqual([]);
    });

    it('should handle empty array', () => {
      expect(sanitizeSpansUI([])).toEqual([]);
    });

    it('should handle mixed valid and invalid spans', () => {
      const input = [
        null,
        { span_id: 'span-1' },
        123,
        { span_id: 'span-2', duration_us: 500 },
        undefined,
        {},
        { span_id: 'span-3' },
      ];
      const result = sanitizeSpansUI(input);
      expect(result).toHaveLength(3);
      expect(result[0].span_id).toBe('span-1');
      expect(result[1].span_id).toBe('span-2');
      expect(result[2].span_id).toBe('span-3');
    });
  });

  describe('sanitizeSpans', () => {
    it('should return empty array when input is not an array', () => {
      expect(sanitizeSpans(null)).toEqual([]);
      expect(sanitizeSpans(undefined)).toEqual([]);
      expect(sanitizeSpans('string')).toEqual([]);
      expect(sanitizeSpans(123)).toEqual([]);
      expect(sanitizeSpans({})).toEqual([]);
    });

    it('should filter out null elements', () => {
      const input = [
        { SpanID: 'span-1', ServiceName: 'service-1' },
        null,
        { SpanID: 'span-2', ServiceName: 'service-2' },
      ];
      const result = sanitizeSpans(input);
      expect(result).toHaveLength(2);
      expect(result[0].SpanID).toBe('span-1');
      expect(result[1].SpanID).toBe('span-2');
    });

    it('should filter out undefined elements', () => {
      const input = [
        { SpanID: 'span-1', ServiceName: 'service-1' },
        undefined,
        { SpanID: 'span-2', ServiceName: 'service-2' },
      ];
      const result = sanitizeSpans(input);
      expect(result).toHaveLength(2);
    });

    it('should filter out invalid spans (primitives)', () => {
      const input = [
        { SpanID: 'span-1', ServiceName: 'service-1' },
        123,
        'string',
        true,
        { SpanID: 'span-2', ServiceName: 'service-2' },
      ];
      const result = sanitizeSpans(input);
      expect(result).toHaveLength(2);
    });

    it('should filter out spans without valid SpanID', () => {
      const input = [
        { SpanID: 'span-1', ServiceName: 'service-1' },
        {}, // no SpanID
        { SpanID: '', ServiceName: 'service-2' }, // empty SpanID
        { SpanID: 'span-3', ServiceName: 'service-3' },
      ];
      const result = sanitizeSpans(input);
      expect(result).toHaveLength(2);
      expect(result[0].SpanID).toBe('span-1');
      expect(result[1].SpanID).toBe('span-3');
    });

    it('should sanitize and return valid spans', () => {
      const input = [
        {
          SpanID: 'span-1',
          ServiceName: 'service-1',
          Duration: 1000,
        },
        {
          SpanID: 'span-2',
          ServiceName: 'service-2',
          Duration: -100, // should be sanitized to 0
        },
      ];
      const result = sanitizeSpans(input);
      expect(result).toHaveLength(2);
      expect(result[0].Duration).toBe(1000);
      expect(result[1].Duration).toBe(0);
    });

    it('should return empty array for array with only invalid elements', () => {
      const input = [null, undefined, 123, 'string', {}];
      const result = sanitizeSpans(input);
      expect(result).toEqual([]);
    });

    it('should handle empty array', () => {
      expect(sanitizeSpans([])).toEqual([]);
    });

    it('should handle mixed valid and invalid spans', () => {
      const input = [
        null,
        { SpanID: 'span-1' },
        123,
        { SpanID: 'span-2', Duration: 500 },
        undefined,
        {},
        { SpanID: 'span-3' },
      ];
      const result = sanitizeSpans(input);
      expect(result).toHaveLength(3);
      expect(result[0].SpanID).toBe('span-1');
      expect(result[1].SpanID).toBe('span-2');
      expect(result[2].SpanID).toBe('span-3');
    });
  });

  describe('detectCircularReferences', () => {
    it('should return empty set for simple linear chain', () => {
      const spans: SpanUI[] = [
        {
          span_id: 'A',
          parent_span_id: '',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 1000,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'B',
          parent_span_id: 'A',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 500,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'C',
          parent_span_id: 'B',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 250,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
      ];
      const cycles = detectCircularReferences(spans);
      expect(cycles.size).toBe(0);
    });

    it('should detect direct self-reference (A->A)', () => {
      const spans: SpanUI[] = [
        {
          span_id: 'A',
          parent_span_id: 'A', // self-reference
          service_name: 'service',
          operation_name: 'op',
          duration_us: 1000,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
      ];
      const cycles = detectCircularReferences(spans);
      expect(cycles.size).toBe(1);
      expect(cycles.has('A')).toBe(true);
    });

    it('should detect two-span cycle (A->B->A)', () => {
      const spans: SpanUI[] = [
        {
          span_id: 'A',
          parent_span_id: 'B',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 1000,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'B',
          parent_span_id: 'A',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 500,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
      ];
      const cycles = detectCircularReferences(spans);
      expect(cycles.size).toBe(2);
      expect(cycles.has('A')).toBe(true);
      expect(cycles.has('B')).toBe(true);
    });

    it('should detect three-span cycle (A->B->C->A)', () => {
      const spans: SpanUI[] = [
        {
          span_id: 'A',
          parent_span_id: 'C',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 1000,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'B',
          parent_span_id: 'A',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 500,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'C',
          parent_span_id: 'B',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 250,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
      ];
      const cycles = detectCircularReferences(spans);
      expect(cycles.size).toBe(3);
      expect(cycles.has('A')).toBe(true);
      expect(cycles.has('B')).toBe(true);
      expect(cycles.has('C')).toBe(true);
    });

    it('should return empty set when there are no cycles', () => {
      const spans: SpanUI[] = [
        {
          span_id: 'A',
          parent_span_id: '',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 1000,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'B',
          parent_span_id: 'A',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 500,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
      ];
      const cycles = detectCircularReferences(spans);
      expect(cycles.size).toBe(0);
    });

    it('should handle empty array', () => {
      const cycles = detectCircularReferences([]);
      expect(cycles.size).toBe(0);
    });

    it('should handle single span with no parent', () => {
      const spans: SpanUI[] = [
        {
          span_id: 'A',
          parent_span_id: '',
          service_name: 'service',
          operation_name: 'op',
          duration_us: 1000,
          start_time: 0,
          status: 'OK',
          attributes: {},
        },
      ];
      const cycles = detectCircularReferences(spans);
      expect(cycles.size).toBe(0);
    });

    it('should detect cycle in complex tree with non-cyclic branches', () => {
      const spans: SpanUI[] = [
        { span_id: 'A', parent_span_id: '', service_name: 's', operation_name: 'o', duration_us: 1, start_time: 0, status: 'OK', attributes: {} },
        { span_id: 'B', parent_span_id: 'A', service_name: 's', operation_name: 'o', duration_us: 1, start_time: 0, status: 'OK', attributes: {} },
        { span_id: 'C', parent_span_id: 'B', service_name: 's', operation_name: 'o', duration_us: 1, start_time: 0, status: 'OK', attributes: {} },
        { span_id: 'D', parent_span_id: 'C', service_name: 's', operation_name: 'o', duration_us: 1, start_time: 0, status: 'OK', attributes: {} },
        { span_id: 'E', parent_span_id: 'D', service_name: 's', operation_name: 'o', duration_us: 1, start_time: 0, status: 'OK', attributes: {} },
        { span_id: 'F', parent_span_id: 'C', service_name: 's', operation_name: 'o', duration_us: 1, start_time: 0, status: 'OK', attributes: {} }, // cycle: F->C
      ];
      // Update to create cycle: C -> B, F -> C creates F -> C -> B
      spans[2].parent_span_id = 'F'; // C points to F
      const cycles = detectCircularReferences(spans);
      expect(cycles.size).toBeGreaterThan(0);
      expect(cycles.has('C')).toBe(true);
      expect(cycles.has('F')).toBe(true);
    });
  });

  describe('detectCircularReferencesSpan', () => {
    it('should return empty set for simple linear chain', () => {
      const spans: Span[] = [
        {
          SpanID: 'A',
          ParentID: '',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 1000,
          StartTime: 0,
          Tags: {},
        },
        {
          SpanID: 'B',
          ParentID: 'A',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 500,
          StartTime: 0,
          Tags: {},
        },
        {
          SpanID: 'C',
          ParentID: 'B',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 250,
          StartTime: 0,
          Tags: {},
        },
      ];
      const cycles = detectCircularReferencesSpan(spans);
      expect(cycles.size).toBe(0);
    });

    it('should detect direct self-reference (A->A)', () => {
      const spans: Span[] = [
        {
          SpanID: 'A',
          ParentID: 'A', // self-reference
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 1000,
          StartTime: 0,
          Tags: {},
        },
      ];
      const cycles = detectCircularReferencesSpan(spans);
      expect(cycles.size).toBe(1);
      expect(cycles.has('A')).toBe(true);
    });

    it('should detect two-span cycle (A->B->A)', () => {
      const spans: Span[] = [
        {
          SpanID: 'A',
          ParentID: 'B',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 1000,
          StartTime: 0,
          Tags: {},
        },
        {
          SpanID: 'B',
          ParentID: 'A',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 500,
          StartTime: 0,
          Tags: {},
        },
      ];
      const cycles = detectCircularReferencesSpan(spans);
      expect(cycles.size).toBe(2);
      expect(cycles.has('A')).toBe(true);
      expect(cycles.has('B')).toBe(true);
    });

    it('should detect three-span cycle (A->B->C->A)', () => {
      const spans: Span[] = [
        {
          SpanID: 'A',
          ParentID: 'C',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 1000,
          StartTime: 0,
          Tags: {},
        },
        {
          SpanID: 'B',
          ParentID: 'A',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 500,
          StartTime: 0,
          Tags: {},
        },
        {
          SpanID: 'C',
          ParentID: 'B',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 250,
          StartTime: 0,
          Tags: {},
        },
      ];
      const cycles = detectCircularReferencesSpan(spans);
      expect(cycles.size).toBe(3);
      expect(cycles.has('A')).toBe(true);
      expect(cycles.has('B')).toBe(true);
      expect(cycles.has('C')).toBe(true);
    });

    it('should return empty set when there are no cycles', () => {
      const spans: Span[] = [
        {
          SpanID: 'A',
          ParentID: '',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 1000,
          StartTime: 0,
          Tags: {},
        },
        {
          SpanID: 'B',
          ParentID: 'A',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 500,
          StartTime: 0,
          Tags: {},
        },
      ];
      const cycles = detectCircularReferencesSpan(spans);
      expect(cycles.size).toBe(0);
    });

    it('should handle empty array', () => {
      const cycles = detectCircularReferencesSpan([]);
      expect(cycles.size).toBe(0);
    });

    it('should handle single span with no parent', () => {
      const spans: Span[] = [
        {
          SpanID: 'A',
          ParentID: '',
          TraceID: 'trace-1',
          ServiceName: 'service',
          OperationName: 'op',
          Duration: 1000,
          StartTime: 0,
          Tags: {},
        },
      ];
      const cycles = detectCircularReferencesSpan(spans);
      expect(cycles.size).toBe(0);
    });

    it('should detect cycle in complex tree with non-cyclic branches', () => {
      const spans: Span[] = [
        { SpanID: 'A', ParentID: '', TraceID: 't', ServiceName: 's', OperationName: 'o', Duration: 1, StartTime: 0, Tags: {} },
        { SpanID: 'B', ParentID: 'A', TraceID: 't', ServiceName: 's', OperationName: 'o', Duration: 1, StartTime: 0, Tags: {} },
        { SpanID: 'C', ParentID: 'B', TraceID: 't', ServiceName: 's', OperationName: 'o', Duration: 1, StartTime: 0, Tags: {} },
        { SpanID: 'D', ParentID: 'C', TraceID: 't', ServiceName: 's', OperationName: 'o', Duration: 1, StartTime: 0, Tags: {} },
        { SpanID: 'E', ParentID: 'D', TraceID: 't', ServiceName: 's', OperationName: 'o', Duration: 1, StartTime: 0, Tags: {} },
        { SpanID: 'F', ParentID: 'C', TraceID: 't', ServiceName: 's', OperationName: 'o', Duration: 1, StartTime: 0, Tags: {} },
      ];
      // Update to create cycle: C -> F creates F -> C -> F
      spans[2].ParentID = 'F'; // C points to F
      const cycles = detectCircularReferencesSpan(spans);
      expect(cycles.size).toBeGreaterThan(0);
      expect(cycles.has('C')).toBe(true);
      expect(cycles.has('F')).toBe(true);
    });
  });

  describe('Constants', () => {
    it('should export DEFAULTS with expected values', () => {
      expect(DEFAULTS.DURATION).toBe(0);
      expect(DEFAULTS.SERVICE).toBe('unknown-service');
      expect(DEFAULTS.OPERATION).toBe('unknown-operation');
      expect(DEFAULTS.STATUS).toBe('OK');
    });

    it('should export MAX_SPAN_DEPTH', () => {
      expect(MAX_SPAN_DEPTH).toBe(100);
      expect(typeof MAX_SPAN_DEPTH).toBe('number');
    });
  });
});

import React from 'react';
import { HelpIcon } from './HelpIcon';

interface CardProps {
  title: string;
  children: React.ReactNode;
  helpText?: string;
}

export function Card({ title, children, helpText }: CardProps) {
  return (
    <div className="bg-slate-800/30 backdrop-blur-sm rounded-xl p-4 border border-slate-700/50">
      <h3 className="text-sm font-semibold text-slate-400 uppercase mb-4 flex items-center">
        {title}
        {helpText && <HelpIcon text={helpText} />}
      </h3>
      {children}
    </div>
  );
}
